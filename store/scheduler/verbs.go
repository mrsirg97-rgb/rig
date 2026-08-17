package scheduler

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	scheddomain "github.com/mrsirg97-rgb/rig/store/scheduler/domain"
)

// --- scope and identity plumbing ---

// Stores is the two-store pairing handed to the verbs by the root: the
// global store and this session's cwd-scoped store, opened once.
type Stores struct {
	Global DB
	Cwd    DB
}

// JobKey is a parsed scheduling key: scope plus id, the hash locating the
// cwd-scope store. One token subsumes <scope> <id>, because a cwd-scope id
// alone cannot address its store.
type JobKey struct {
	Scope string // "global" | "cwd"
	Hash  string // "" for global; 12-hex for cwd
	ID    string
}

var (
	keyRe       = regexp.MustCompile(`^cwd-([0-9a-f]{12}):(j\d+)$`)
	globalKeyRe = regexp.MustCompile(`^(j\d+)$`)
	hashRe      = regexp.MustCompile(`^[0-9a-f]{12}$`)
)

// KeyOf composes the scheduling key.
func KeyOf(scope, hash, id string) (string, error) {
	if scope == "global" {
		return id, nil
	}
	if !hashRe.MatchString(hash) {
		return "", fmt.Errorf("key: cwd scope needs a 12-hex hash, got '%s'", hash)
	}
	return fmt.Sprintf("cwd-%s:%s", hash, id), nil
}

// ParseKey decomposes a scheduling key. Garbage refuses.
func ParseKey(key string) (JobKey, error) {
	var out JobKey
	if m := keyRe.FindStringSubmatch(key); m != nil {
		out.Scope, out.Hash, out.ID = "cwd", m[1], m[2]
		return out, nil
	}
	if m := globalKeyRe.FindStringSubmatch(key); m != nil {
		out.Scope, out.ID = "global", m[1]
		return out, nil
	}
	return out, fmt.Errorf("key: bad key '%s' (want j<n> or cwd-<12hex>:j<n>)", key)
}

// StorePathFor: the scheduler home's file for one parsed key.
func StorePathFor(home string, key JobKey) string {
	if key.Hash != "" {
		return filepath.Join(home, key.Hash+".sqlite")
	}
	return filepath.Join(home, "global.sqlite")
}

// ScopeDir names the runs/<scope>/ directory: global, or cwd-<hash>.
func ScopeDir(key JobKey) string {
	if key.Hash != "" {
		return "cwd-" + key.Hash
	}
	return "global"
}

// CwdHash is the workspace digest prefix: the first twelve hex digits of
// sha1(cwd) — the store file's locator.
func CwdHash(cwd string) string {
	sum := sha1.Sum([]byte(cwd))
	return hex.EncodeToString(sum[:])[:12]
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func scopeName(scope string) string {
	if scope == "global" {
		return "global"
	}
	return "cwd"
}

func scopeHash(scope, sessionCwd string) string {
	if scope == "global" {
		return ""
	}
	return CwdHash(sessionCwd)
}

// --- create ---

// CreateInput is one create payload (scope "" is the cwd scope).
type CreateInput struct {
	Name   string
	Prompt string
	Cron   string
	At     string
	Scope  string
	Cwd    string
	Model  string
	Busy   string
}

const defaultModel = "qwen3.8-workers"

// schedErr shapes the store voice: the "scheduler: ..." prefix.
func schedErr(format string, a ...any) error {
	return fmt.Errorf("scheduler: "+format, a...)
}

// Create schedules one job: crontab first (the visible failure mode is a
// line with no row), then the store mutation.
func Create(ctx context.Context, st Stores, ct Crontab, in CreateInput, sessionCwd, session, runnerCmd string, now func() time.Time) (string, error) {
	if now == nil {
		now = time.Now
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return "", schedErr("create requires a non-empty name")
	}
	prompt := in.Prompt
	if prompt == "" {
		return "", schedErr("create requires a non-empty prompt")
	}
	scope := scopeName(in.Scope)
	db := st.Cwd
	if scope == "global" {
		db = st.Global
	}
	// where the job RUNS: explicit, else the creating session's cwd — in
	// both scopes a job always has a working directory
	jobCwd := in.Cwd
	if jobCwd == "" {
		jobCwd = sessionCwd
	}
	if jobCwd == "" {
		return "", schedErr("create requires a working directory (cwd or a session cwd)")
	}
	model := in.Model
	if model == "" {
		model = defaultModel
	}
	busy := busyOf(in.Busy)

	cron := strings.TrimSpace(in.Cron)
	if cron == "" {
		return "", schedErr("create requires 'cron' (5-field or 'once' + 'at')")
	}
	var at *string
	if cron == "once" {
		if in.At == "" {
			return "", schedErr("cron 'once' requires 'at' (ISO time)")
		}
		t, err := time.Parse(time.RFC3339, in.At)
		if err != nil {
			return "", schedErr("'at' must be a valid ISO time, got '%s'", in.At)
		}
		norm := t.UTC().Format(time.RFC3339)
		at = &norm
		// cron fires in local time; the runner's minute-granularity is the
		// contract
		lt := t.Local()
		cron = fmt.Sprintf("%d %d %d %d *", lt.Minute(), lt.Hour(), lt.Day(), int(lt.Month()))
	} else {
		if _, err := ValidateCron(cron); err != nil {
			return "", err // `cron: ...` before anything is written
		}
	}

	// read phase: duplicate-name check (live jobs only) and the candidate
	// id the crontab key rides
	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx)
	if err != nil {
		return "", err
	}
	for _, j := range f.jobs {
		if j.State != "removed" && j.Name == name {
			return "", schedErr("a job named '%s' already exists in scope '%s' (state: %s); remove it first", name, scope, j.State)
		}
	}
	candidateID := f.mintID()
	key, err := KeyOf(scope, scopeHash(scope, sessionCwd), candidateID)
	if err != nil {
		return "", err
	}

	// crontab first: the visible failure mode is a line with no row
	text, err := ct.List()
	if err != nil {
		return "", err
	}
	next, _ := UpsertLine(text, key, cron, runnerCmd)
	if err := ct.Install(next); err != nil {
		return "", err
	}

	// store mutation: replay fresh, compact check, event, projection
	if err := maybeCompact(bound, tx, f, session); err != nil {
		return "", err
	}
	argsJSON, _ := json.Marshal(map[string]any{
		"name": name, "prompt": prompt, "cron": cron, "at": at,
		"cwd": jobCwd, "model": model, "busy": busy,
	})
	seq, err := appendEvent(bound, f.maxSeq+1, "create", string(argsJSON), session)
	if err != nil {
		return "", err
	}
	f.apply(eventRow{seq: seq, ts: nowRFC3339(), op: "create", args: string(argsJSON)})
	created := createdRow(f, seq)
	if created == nil {
		return "", schedErr("concurrent create raced on name '%s'; the crontab line is orphaned, remove it", name)
	}
	if err := rewrite(tx, f); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	drift := ""
	if created.ID != candidateID {
		drift = fmt.Sprintf("  drift: crontab line carries key for %s, store row is %s (concurrent create); remove and re-create", candidateID, created.ID)
	}
	line := TaggedLine{Key: key, Cron: created.Cron, Paused: false}
	lines := jobLines(created, scope, &line, false, now)
	if drift != "" {
		lines = append(lines, drift)
	}
	return replyText(fmt.Sprintf("created %s '%s' (%s)", created.ID, created.Name, scope), lines), nil
}

// createdRow finds the job this create event produced: live, and updated by
// exactly this event's seq.
func createdRow(f *fold, seq int64) *jobState {
	for _, j := range f.jobs {
		if j.State != "removed" && j.UpdatedSeq == seq {
			return j
		}
	}
	return nil
}

// --- state actions (pause / resume / remove) ---

// Pause/Resume/Remove: the crontab write lands before the store mutation;
// a drift (line missing) still changes the store state — list flags it
// afterward.
func Pause(ctx context.Context, st Stores, ct Crontab, id, scope, sessionCwd, session string) (string, error) {
	return stateAction(ctx, st, ct, id, scope, sessionCwd, session, "pause")
}

func Resume(ctx context.Context, st Stores, ct Crontab, id, scope, sessionCwd, session string) (string, error) {
	return stateAction(ctx, st, ct, id, scope, sessionCwd, session, "resume")
}

func Remove(ctx context.Context, st Stores, ct Crontab, id, scope, sessionCwd, session string) (string, error) {
	return stateAction(ctx, st, ct, id, scope, sessionCwd, session, "remove")
}

func stateAction(ctx context.Context, st Stores, ct Crontab, id, scope, sessionCwd, session, action string) (string, error) {
	db, job, found, err := resolveJob(ctx, st, id, scope)
	if err != nil {
		return "", err
	}
	if !found {
		if scope != "" {
			return "", schedErr("no job '%s' in scope '%s'", id, scopeName(scope))
		}
		return "", schedErr("no job '%s'", id)
	}
	jobScope := jobScopeOf(st, db)
	switch action {
	case "pause":
		if job.State == "paused" {
			return "", schedErr("'%s' is already paused", id)
		}
		if job.State == "done" {
			return "", schedErr("'%s' is done; nothing to pause", id)
		}
	case "resume":
		if job.State != "paused" {
			return "", schedErr("'%s' is not paused", id)
		}
	case "remove":
		if job.State == "removed" {
			return "", schedErr("'%s' is already removed", id)
		}
	}

	key, err := KeyOf(jobScope, scopeHash(jobScope, sessionCwd), id)
	if err != nil {
		return "", err
	}
	// crontab first: a failed crontab write never leaves a store mutation.
	text, err := ct.List()
	if err != nil {
		return "", err
	}
	var op func(string) (string, bool)
	switch action {
	case "pause":
		op = func(t string) (string, bool) { return SetPaused(t, key, true) }
	case "resume":
		op = func(t string) (string, bool) { return SetPaused(t, key, false) }
	case "remove":
		op = func(t string) (string, bool) { return RemoveLine(t, key) }
	}
	next, foundLine := op(text)
	if foundLine && next != text {
		if err := ct.Install(next); err != nil {
			return "", err
		}
	}

	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx)
	if err != nil {
		return "", err
	}
	if err := maybeCompact(bound, tx, f, session); err != nil {
		return "", err
	}
	argsJSON, _ := json.Marshal(map[string]any{"id": id})
	seq, err := appendEvent(bound, f.maxSeq+1, action, string(argsJSON), session)
	if err != nil {
		return "", err
	}
	f.apply(eventRow{seq: seq, ts: nowRFC3339(), op: action, args: string(argsJSON)})
	row := f.jobs[id]
	if err := rewrite(tx, f); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	var line *TaggedLine
	if action != "remove" {
		line = &TaggedLine{Key: key, Cron: row.Cron, Paused: action == "pause"}
	}
	lines := jobLines(row, jobScope, line, false, time.Now)
	return replyText(fmt.Sprintf("%s %s -> %s", row.ID, row.Name, row.State), lines), nil
}

// jobScopeOf names which scope store this handle is (for key derivation:
// the key rides the workspace that created the job).
func jobScopeOf(st Stores, db DB) string {
	if db.DB == st.Global.DB {
		return "global"
	}
	return "cwd"
}

// --- runs ---

// RunRecordInput is one run record, including the runs container's
// started/ended pair.
type RunRecordInput struct {
	ID       string
	Status   string
	Exit     *int64
	Duration *int64
	Log      string
	Reason   string
	Started  string
	Ended    string
}

// RecordRun lands one run record: the run event (the jobs projection's
// last_* rides it), the structured runs row (seq-paired with the event, so
// history survives compaction), and the jobs update. One transaction.
func RecordRun(ctx context.Context, db DB, in RunRecordInput) (int64, error) {
	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx)
	if err != nil {
		return 0, err
	}
	status := statusOf(in.Status)
	argsJSON, _ := json.Marshal(map[string]any{
		"id": in.ID, "status": status, "exit": in.Exit,
		"durationMs": in.Duration, "log": in.Log, "reason": in.Reason,
	})
	seq, err := appendEvent(bound, f.maxSeq+1, "run", string(argsJSON), "")
	if err != nil {
		return 0, err
	}
	job, ok := f.jobs[in.ID]
	if ok {
		job.LastStatus = status
		if in.Exit != nil {
			job.LastExit = *in.Exit
			job.LastExitSet = true
		}
		job.UpdatedSeq = seq
	}
	var reason, log *string
	if in.Reason != "" {
		r := in.Reason
		reason = &r
	}
	if in.Log != "" {
		l := in.Log
		log = &l
	}
	started := in.Started
	if started == "" {
		started = nowRFC3339()
	}
	ended := in.Ended
	if ended == "" {
		ended = nowRFC3339()
	}
	if _, err := scheddomain.NewRunDomain().InsertRun(bound, scheddomain.Run{
		Seq: seq, JobId: in.ID,
		StartedAt: started, EndedAt: ended,
		Status: status, Exit: in.Exit, DurationMs: in.Duration,
		Reason: reason, LogPath: log,
	}); err != nil {
		return 0, fmt.Errorf("scheduler: run record: %w", err)
	}
	if ok {
		if err := rewrite(tx, f); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return seq, nil
}

func statusOf(s string) string {
	switch s {
	case "ok", "fail", "skip":
		return s
	}
	return "skip"
}

// runRecord is one runs-container row as read.
type runRecord struct {
	Seq        int64
	Started    string
	Status     string
	Exit       *int64
	DurationMs *int64
	Reason     *string
	LogPath    *string
}

// Runs is the audit trail: the last n records, oldest first, a chain read
// over the runs container (SPEC_STATE's deviation).
func Runs(ctx context.Context, st Stores, id, scope string, n int) (string, error) {
	if n <= 0 {
		n = 5
	}
	if n < 1 || n > 100 {
		return "", schedErr("runs count must be an integer 1-100, got %d", n)
	}
	db, _, found, err := resolveJob(ctx, st, id, scope)
	if err != nil {
		return "", err
	}
	if !found {
		if scope != "" {
			return "", schedErr("no job '%s' in scope '%s'", id, scopeName(scope))
		}
		return "", schedErr("no job '%s'", id)
	}
	_, tx, err := db.TxReadOnly(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	// the runs read: a bounded seek over this job's rows, newest first —
	// the narrow indexed seek SPEC_STATE names for the container.
	// Named raw arm.
	rows, err := tx.Query(`SELECT seq, started_at, status, exit, duration_ms, reason, log_path FROM runs WHERE job_id = ? ORDER BY seq DESC LIMIT ?`, id, n)
	if err != nil {
		return "", fmt.Errorf("scheduler: runs: %w", err)
	}
	defer rows.Close()
	var out []runRecord
	for rows.Next() {
		var r runRecord
		if err := rows.Scan(&r.Seq, &r.Started, &r.Status, &r.Exit, &r.DurationMs, &r.Reason, &r.LogPath); err != nil {
			return "", fmt.Errorf("scheduler: runs: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("scheduler: runs: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	if len(out) == 0 {
		return fmt.Sprintf("%s · 0 runs (oldest first):", id), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %d run%s (oldest first):\n", id, len(out), plural(len(out)))
	for _, r := range out {
		fmt.Fprintf(&b, "%s  %s  %s\n", r.Started, r.Status, runDetail(r))
	}
	return b.String(), nil
}

func runDetail(r runRecord) string {
	if r.Status == "skip" {
		if r.Reason != nil {
			return *r.Reason
		}
		return ""
	}
	var bits []string
	if r.Exit != nil {
		bits = append(bits, fmt.Sprintf("exit %d", *r.Exit))
	}
	if r.DurationMs != nil {
		bits = append(bits, fmt.Sprintf("%dms", *r.DurationMs))
	}
	if r.LogPath != nil {
		bits = append(bits, *r.LogPath)
	}
	return strings.Join(bits, " ")
}

// --- resolution ---

// resolveJob resolves an id to exactly one (store, job). Unknown ids
// refuse; ids present in both scopes refuse without an explicit scope.
// Tombstones resolve too (runs works after remove).
func resolveJob(ctx context.Context, st Stores, id, scope string) (DB, *jobState, bool, error) {
	var dbs []struct {
		scope string
		db    DB
	}
	switch scope {
	case "global":
		dbs = []struct {
			scope string
			db    DB
		}{{"global", st.Global}}
	case "cwd":
		dbs = []struct {
			scope string
			db    DB
		}{{"cwd", st.Cwd}}
	default:
		// no scope: global first, then cwd; both present is ambiguous
		dbs = []struct {
			scope string
			db    DB
		}{
			{"global", st.Global},
			{"cwd", st.Cwd},
		}
	}
	var hits []struct {
		db  DB
		job *jobState
	}
	for _, d := range dbs {
		_, tx, err := d.db.TxReadOnly(ctx)
		if err != nil {
			return DB{}, nil, false, err
		}
		f, err := eventsOf(tx)
		if err != nil {
			tx.Rollback()
			return DB{}, nil, false, err
		}
		tx.Rollback()
		if j, ok := f.jobs[id]; ok {
			hits = append(hits, struct {
				db  DB
				job *jobState
			}{d.db, j})
		}
	}
	switch len(hits) {
	case 0:
		return DB{}, nil, false, nil
	case 1:
		return hits[0].db, hits[0].job, true, nil
	default:
		return DB{}, nil, false, schedErr("'%s' is ambiguous: it exists in both scopes; pass scope ('global' or 'cwd')", id)
	}
}
