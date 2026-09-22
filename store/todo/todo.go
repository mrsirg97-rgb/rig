package todo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	tododdl "github.com/mrsirg97-rgb/rig/store/todo/ddl"
	tododomain "github.com/mrsirg97-rgb/rig/store/todo/domain"
	todometa "github.com/mrsirg97-rgb/rig/store/todo/metadata"
)

const SchemaVersion = 3

func DDL() []string { return tododdl.Statements() }

func Statements() []string {
	out := tododdl.Statements()
	return append(out, todometa.ExtraStatements()...)
}

const anon = "anon"

// StaleClaimAfter is the age after which a foreign claim may be
// released: the owner's last event on the task is older than this, so
// the session that held it is gone or gone quiet. The ended-session
// arm of Reap does not wait on it — a session row that closed is dead
// on arrival.
const StaleClaimAfter = 24 * time.Hour

// Project is one queue's identity: Key partitions the log, Label names
// the queue in every reply so a reader can never mistake whose queue a
// line belongs to. OutsideRepo marks a bucket minted from a directory
// that is not a repo (the launch-cwd hash): shared by every session
// started there, it is a place, not a project, and the reply says so.
type Project struct {
	Key         string
	Label       string
	OutsideRepo bool
}

type CreateItem struct {
	Text      string
	DependsOn *string
	DepNull   bool
}

func (c CreateItem) raw() rawItem {
	it := rawItem{text: c.Text}
	if c.DepNull {
		it.hasDep, it.depNull = true, true
		return it
	}
	if c.DependsOn != nil {
		it.hasDep = true
		it.dep = *c.DependsOn
	}
	return it
}

type noteState struct {
	text    string
	session string
}

type taskState struct {
	id         string
	text       string
	status     string
	pos        int
	dep        string
	owner      string
	createdSeq int64
	updatedSeq int64
	updatedTs  string
	notes      []noteState
}

type folded struct {
	compactSeq int64
	tasks      map[string]*taskState
	maxSeq     int64
	maxPos     int
	maxIdNum   int
	globalSeq  int64
	label      string
	notRepo    bool
}

func newFolded() *folded {
	return &folded{tasks: map[string]*taskState{}}
}

func (f *folded) byText(text string) *taskState {
	for _, ts := range f.tasks {
		if ts.text == text {
			return ts
		}
	}
	return nil
}

func (f *folded) mintID() string {
	for {
		f.maxIdNum++
		id := fmt.Sprintf("t%d", f.maxIdNum)
		if _, ok := f.tasks[id]; !ok {
			return id
		}
	}
}

func (f *folded) nextPos() int {
	f.maxPos++
	return f.maxPos
}

func (f *folded) nextSeq() int64 {
	f.globalSeq++
	return f.globalSeq
}

type eventRow struct {
	seq     int64
	op      string
	args    string
	session string
	ts      string
	scope   string
}

func attrOf(e eventRow) string {
	if e.session == "" {
		return anon
	}
	return e.session
}

func (f *folded) apply(e eventRow) {
	if e.seq > f.maxSeq {
		f.maxSeq = e.seq
	}
	if e.seq > f.globalSeq {
		f.globalSeq = e.seq
	}
	switch e.op {
	case "create":
		f.applyCreate(e)
	case "start", "complete", "fail", "release", "retry":
		f.applyVerb(e)
	case "claim":
		f.applyClaimEvent(e)
	case "note":
		f.applyNoteEvent(e)
	case "accept":
		f.applyAcceptEvent(e)
	case "reject":
		f.applyRejectEvent(e)
	case "move":
		f.applyMoveEvent(e)
	case "prune":
		f.applyPrune()
	case "compact":
		f.applyCompactEvent(e)
	}
}

func (f *folded) applyClaimEvent(e eventRow) {
	var payload struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(e.args), &payload) != nil || payload.ID == "" {
		return
	}
	ts, ok := f.tasks[payload.ID]
	if !ok {
		return
	}
	switch ts.status {
	case statusPending:
		ts.status = statusActive
		ts.owner = attrOf(e)
	case statusReview:
		ts.owner = attrOf(e)
	default:
		return
	}
	ts.updatedSeq = e.seq
	ts.updatedTs = e.ts
}

func (f *folded) applyNoteEvent(e eventRow) {
	var payload struct {
		ID   string `json:"id"`
		Note string `json:"note"`
	}
	if json.Unmarshal([]byte(e.args), &payload) != nil || payload.ID == "" || payload.Note == "" {
		return
	}
	ts, ok := f.tasks[payload.ID]
	if !ok {
		return
	}
	ts.notes = append(ts.notes, noteState{text: payload.Note, session: attrOf(e)})
}

func (f *folded) applyAcceptEvent(e eventRow) {
	var payload struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(e.args), &payload) != nil || payload.ID == "" {
		return
	}
	ts, ok := f.tasks[payload.ID]
	if !ok || ts.status != statusReview {
		return
	}
	ts.status = statusDone
	ts.owner = ""
	ts.updatedSeq = e.seq
	ts.updatedTs = e.ts
}

func (f *folded) applyRejectEvent(e eventRow) {
	var payload struct {
		ID   string `json:"id"`
		Note string `json:"note"`
	}
	if json.Unmarshal([]byte(e.args), &payload) != nil || payload.ID == "" {
		return
	}
	ts, ok := f.tasks[payload.ID]
	if !ok || ts.status != statusReview {
		return
	}
	ts.status = statusPending
	ts.owner = ""
	if payload.Note != "" {
		ts.notes = append(ts.notes, noteState{text: payload.Note, session: attrOf(e)})
	}
	ts.updatedSeq = e.seq
	ts.updatedTs = e.ts
}

type rawItem struct {
	text    string
	hasDep  bool
	depNull bool
	dep     string
}

func decodeItems(args string) ([]rawItem, bool) {
	var payload struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if json.Unmarshal([]byte(args), &payload) != nil {
		return nil, false
	}
	var out []rawItem
	for _, raw := range payload.Tasks {
		text, _ := raw["text"].(string)
		it := rawItem{text: text}
		if v, ok := raw["dependsOn"]; ok {
			it.hasDep = true
			if v == nil {
				it.depNull = true
			} else if s, ok := v.(string); ok {
				it.dep = s
			}
		}
		out = append(out, it)
	}
	return out, true
}

func (f *folded) applyCreate(e eventRow) {
	items, ok := decodeItems(e.args)
	if !ok {
		return
	}
	if len(items) == 0 {
		f.tasks = map[string]*taskState{}
		return
	}
	type pendingRef struct {
		ts    *taskState
		clear bool
		ref   string
	}
	var refs []pendingRef
	seen := map[string]bool{}
	preIDs := depPreIDs(f)
	batchTexts := map[string]*taskState{}
	for _, item := range items {
		if item.text == "" || seen[item.text] {
			continue
		}
		seen[item.text] = true
		ex := f.byText(item.text)
		if ex != nil {
			if item.hasDep {
				refs = append(refs, pendingRef{ts: ex, clear: item.depNull, ref: item.dep})
			}
			continue
		}
		ts := &taskState{
			text: item.text, status: statusPending,
			createdSeq: e.seq, updatedSeq: e.seq, updatedTs: e.ts,
		}
		ts.id = f.mintID()
		ts.pos = f.nextPos()
		f.tasks[ts.id] = ts
		batchTexts[item.text] = ts
		if item.
			hasDep {
			refs = append(refs, pendingRef{ts: ts, clear: item.depNull, ref: item.dep})
		}
	}

	for _, pr := range refs {
		if pr.clear {
			pr.ts.dep = ""
			continue
		}
		if ref := resolveDep(preIDs, batchTexts, f, pr.ref); ref != "" {
			pr.ts.dep = ref
		}
	}
}

func depPreIDs(f *folded) map[string]bool {
	out := map[string]bool{}
	for id := range f.tasks {
		out[id] = true
	}
	return out
}

func resolveDep(preIDs map[string]bool, batchTexts map[string]*taskState, f *folded, raw string) string {
	if preIDs[raw] {
		return raw
	}
	if ts, ok := batchTexts[raw]; ok {
		return ts.id
	}
	if ts := f.byText(raw); ts != nil {
		return ts.id
	}
	return ""
}

func (f *folded) applyVerb(e eventRow) {
	var payload struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(e.args), &payload) != nil || payload.ID == "" {
		return
	}
	ts, ok := f.tasks[payload.ID]
	if !ok {
		return
	}
	switch e.op {
	case "start":
		if ts.status == "pending" {
			ts.status = "in_progress"
			ts.owner = attrOf(e)
		}
	case "complete":
		if ts.status == "in_progress" {
			ts.status = "review"
			ts.owner = ""
		}
	case "fail":
		if ts.status == "in_progress" {
			ts.status = "failed"
			ts.owner = ""
		}
	case "release":
		if ts.status == "in_progress" {
			ts.status = "pending"
			ts.owner = ""
		}
	case "retry":
		if ts.status == "failed" {
			ts.status = "pending"
		}
	}
	ts.updatedSeq = e.seq
	ts.updatedTs = e.ts
}

func planCreate(f *folded, items []CreateItem) (modified []*taskState, given, fresh int, problems []string) {
	planned := map[string]*taskState{}
	type depRef struct {
		ts      *taskState
		text    string
		depNull bool
		dep     string
	}
	var refs []depRef
	seen := map[string]bool{}
	preIDs := depPreIDs(f)
	for _, item := range items {
		raw := item.raw()
		if raw.text == "" || seen[raw.text] {
			continue
		}
		seen[raw.text] = true
		given++
		ex := f.byText(raw.text)
		if ex != nil {
			planned[raw.text] = ex
			if raw.hasDep {
				refs = append(refs, depRef{ts: ex, text: raw.text, depNull: raw.depNull, dep: raw.dep})
			}
			continue
		}
		fresh++
		ts := &taskState{text: raw.text, status: statusPending}
		ts.id = f.mintID()
		ts.pos = f.nextPos()
		planned[raw.text] = ts
		f.tasks[ts.id] = ts
		modified = append(modified, ts)
		if raw.hasDep {
			refs = append(refs, depRef{ts: ts, text: raw.text, depNull: raw.depNull, dep: raw.dep})
		}
	}

	for _, dr := range refs {
		if dr.depNull {
			dr.ts.dep = ""
			continue
		}
		if dr.dep == dr.text {
			addOnce(&problems, fmt.Sprintf("'%s' cannot depend on itself", dr.text))
			continue
		}
		if resolved := resolveDep(preIDs, planned, f, dr.dep); resolved != "" {
			dr.ts.dep = resolved
			modified = append(modified, dr.ts)
		} else {
			addOnce(&problems, fmt.Sprintf("dependsOn '%s' not found", dr.dep))
		}
	}
	if path := cyclePath(f, planned); path != nil {
		problems = append(problems, "dependencies would form a cycle: "+strings.Join(path, " -> "))
	}
	return modified, given, fresh, problems
}

func addOnce(list *[]string, s string) {
	for _, have := range *list {
		if have == s {
			return
		}
	}
	*list = append(*list, s)
}

func cyclePath(f *folded, planned map[string]*taskState) []string {
	adj := map[string][]string{}
	for id, ts := range f.tasks {
		if ts.dep != "" {
			adj[id] = append(adj[id], ts.dep)
		}
	}
	var cycle []string
	var stack []string
	onStack := map[string]bool{}
	var dfs func(id string) bool
	dfs = func(id string) bool {
		onStack[id] = true
		stack = append(stack, id)
		for _, dep := range adj[id] {
			if onStack[dep] {
				for i, n := range stack {
					if n == dep {
						cycle = append([]string{dep}, stack[i:]...)
						return true
					}
				}
				continue
			}
			if _, ok := f.tasks[dep]; !ok {
				continue
			}
			if dfs(dep) {
				return true
			}
		}
		onStack[id] = false
		stack = stack[:len(stack)-1]
		return false
	}
	var ids []string
	for id := range planned {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ts := planned[id]
		if dfs(ts.id) {
			return cycle
		}
	}
	return nil
}

const (
	statusPending = "pending"
	statusActive  = "in_progress"
	statusReview  = "review"
	statusDone    = "done"
	statusFailed  = "failed"
)

func marker(status string) string {
	switch status {
	case statusDone:
		return "[x]"
	case statusFailed:
		return "[!]"
	case statusReview:
		return "[r]"
	case statusActive:
		return "[~]"
	default:
		return "[ ]"
	}
}

func orderedTaskStates(f *folded) []*taskState {
	out := make([]*taskState, 0, len(f.tasks))
	for _, ts := range f.tasks {
		out = append(out, ts)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].pos != out[j].pos {
			return out[i].pos < out[j].pos
		}
		return out[i].createdSeq < out[j].createdSeq
	})
	return out
}

func blockedBy(f *folded, ts *taskState) string {
	if ts.status != statusPending && ts.status != statusActive {
		return ""
	}
	if ts.dep == "" {
		return ""
	}
	dep := f.tasks[ts.dep]
	if dep == nil || dep.status == statusDone {
		return ""
	}
	return ts.dep
}

func blockHint(f *folded, depID string) string {
	switch f.tasks[depID].status {
	case statusPending:
		return "pending; start it first"
	case statusFailed:
		return "failed; retry it first"
	default:
		return "in_progress"
	}
}

func claimSuffix(ts *taskState, session string) string {
	if ts.status != statusActive && ts.status != statusReview {
		return ""
	}
	if ts.owner == "" || ts.owner == session {
		return ""
	}
	owner := ts.owner
	if len(owner) > 8 {
		owner = owner[:8]
	}
	verb := "claimed by"
	if ts.status == statusReview {
		verb = "claimed for review by"
	}
	return " \u00b7 " + verb + " " + owner
}

func staleFooter(f *folded) string {
	if f.maxSeq <= STALE_THRESHOLD_SEQ {
		return ""
	}
	n := 0
	latest := ""
	for _, ts := range f.tasks {
		if ts.status != statusPending && ts.status != statusActive && ts.status != statusReview {
			continue
		}
		if ts.updatedSeq > f.maxSeq-STALE_THRESHOLD_SEQ {
			continue
		}
		n++
		if ts.updatedTs > latest {
			latest = ts.updatedTs
		}
	}
	if n == 0 {
		return ""
	}
	if len(latest) > 10 {
		latest = latest[:10]
	}
	return fmt.Sprintf("\u00b7 %d unresolved since %s (recovered from log)", n, latest)
}

func summaryOf(f *folded) string {
	ordered := orderedTaskStates(f)
	done, failed, review := 0, 0, 0
	nextID := ""
	for _, ts := range ordered {
		switch ts.status {
		case statusDone:
			done++
		case statusFailed:
			failed++
		case statusReview:
			review++
		}
	}
	for _, ts := range ordered {
		if ts.status == statusPending && blockedBy(f, ts) == "" {
			nextID = ts.id
			break
		}
	}
	var b strings.Builder
	b.WriteString(scopeTag(f))
	fmt.Fprintf(&b, "%d/%d done", done, len(ordered))
	if nextID != "" {
		fmt.Fprintf(&b, " \u00b7 next: %s", nextID)
	}
	if review != 0 {
		fmt.Fprintf(&b, " \u00b7 %d in review", review)
	}
	if failed != 0 {
		fmt.Fprintf(&b, " \u00b7 %d failed", failed)
	}
	return b.String()
}

// scopeTag names the queue a reply speaks about. Every summary carries
// it: a queue is addressed by project, and a session launched outside a
// repo can be looking at any of several buckets, so a reply that could
// be read two ways carries the word that picks one (SPEC_CORE).
func scopeTag(f *folded) string {
	if f.label == "" {
		return ""
	}
	if f.notRepo {
		return "[" + f.label + " (not a repo)] "
	}
	return "[" + f.label + "] "
}

func lineOf(f *folded, ts *taskState, session string) string {
	line := fmt.Sprintf("  %s %s %s", ts.id, marker(ts.status), ts.text)
	if blocker := blockedBy(f, ts); blocker != "" {
		line += fmt.Sprintf(" \u00b7 waits on %s", blocker)
	}
	line += claimSuffix(ts, session)
	return line
}

func renderTask(f *folded, ts *taskState, session string) string {
	line := lineOf(f, ts, session)
	for _, n := range ts.notes {
		line += "\n    \u00b7 " + n.text + " (by " + n.session + ")"
	}
	return line
}

func renderQueue(f *folded, session string, all bool, label string) string {
	ordered := orderedTaskStates(f)
	if len(ordered) == 0 {
		if f.notRepo && label != "" {
			return fmt.Sprintf("(no tasks in %s's queue, not a repo)", label)
		}
		return fmt.Sprintf("(no tasks in %s's queue)", label)
	}
	var b strings.Builder
	b.WriteString(summaryOf(f))
	for _, ts := range ordered {
		if !all && ts.status == statusDone {
			continue
		}
		b.WriteString("\n" + renderTask(f, ts, session))
	}
	return b.String()
}

func echoTask(f *folded, session, id, note string) string {
	var b strings.Builder
	if note != "" {
		fmt.Fprintf(&b, "\u2192 %s\n", note)
	}
	if ts := f.tasks[id]; ts != nil {
		b.WriteString(renderTask(f, ts, session))
	}
	b.WriteString("\n" + summaryOf(f))
	if foot := staleFooter(f); foot != "" {
		b.WriteString("\n" + foot)
	}
	return b.String()
}

// Create folds tasks into the queue: an item whose text matches a row
// already in the queue keeps that row (id, status, position), a new text
// mints one. It is a merge on the text natural key, not a wipe: only the
// empty list clears. That is what lets a later session depend on an
// earlier task, and what makes the note's counts the whole story.
func Create(ctx context.Context, db store.DB, p Project, items []CreateItem, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		modified, given, fresh, problems := planCreate(f, items)
		if len(problems) != 0 {
			sort.Strings(problems)
			return "", fmt.Errorf("todo: %s", strings.Join(problems, "; "))
		}
		note := mergeNote(given, fresh)
		if len(items) == 0 {
			f.tasks = map[string]*taskState{}
			note = "queue cleared"
		}
		args, _ := json.Marshal(map[string]any{"tasks": asGiven(items)})
		seq := f.nextSeq()
		if e := appendEvent(bound, seq, "create", string(args), session, p.Key); e != nil {
			return "", e
		}
		for _, ts := range modified {
			ts.updatedSeq = seq
			ts.updatedTs = nowRFC3339()
		}
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(replyText(f, session, note, false, p.Label), foot), nil
	})
}

// mergeNote says what a create did in the numbers that matter: a merge
// that reports "replaced" teaches the wrong model of the queue.
func mergeNote(given, fresh int) string {
	switch {
	case given == 0:
		return "queue unchanged: no task text given"
	case fresh == 0:
		return "queue merged: nothing new"
	case given-fresh == 0:
		return "queue merged: " + strconv.Itoa(fresh) + " new"
	default:
		return "queue merged: " + strconv.Itoa(fresh) + " new, " + strconv.Itoa(given-fresh) + " already there"
	}
}

func Start(ctx context.Context, db store.DB, p Project, id, session string) (string, error) {
	return verb(ctx, db, p, session, id, func(f *folded, ts *taskState) (ok bool, voice string) {
		switch ts.status {
		case statusPending:
			return true, ""
		case statusActive:
			voice := "'" + id + "' is already in progress"
			if ts.owner != "" {
				voice += " (claimed by " + ts.owner + ")"
			}
			return false, voice
		case statusReview:
			return false, "'" + id + "' is in review; accept or reject it first"
		case statusDone:
			return false, "'" + id + "' is done; read-only"
		default:
			return false, "'" + id + "' failed; retry it first"
		}
	}, "start", statusActive, "'"+id+"' started")
}

// Complete moves the caller's task out of active. On the caller's own
// unclaimed pending task it implicitly claims and completes (start+complete,
// both events appended, the echo noting the auto-start). worker=false lands
// the task done in one call (complete+accept, the log stays uniform);
// worker=true submits it for review, and the parent's accept or reject
// finishes it.
func Complete(ctx context.Context, db store.DB, p Project, id, session string, worker bool) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		switch ts.status {
		case statusReview:
			return "", fmt.Errorf("'%s' is in review; accept or reject it first", id)
		case statusDone:
			return "", fmt.Errorf("'%s' is done; read-only", id)
		case statusFailed:
			return "", fmt.Errorf("'%s' failed; retry it first", id)
		}
		if ts.owner != "" && ts.owner != session {
			return "", fmt.Errorf("'%s' is claimed by %s; fail it first to take over", id, ts.owner)
		}
		if blocker := blockedBy(f, ts); blocker != "" {
			return "", fmt.Errorf("'%s' is blocked by '%s' (%s)", id, blocker, blockHint(f, blocker))
		}

		args, _ := json.Marshal(map[string]any{"id": id})
		note := "'" + id + "' completed"
		if worker {
			note += "; in review"
		}
		if ts.status == statusPending {
			startSeq := f.nextSeq()
			if e := appendEvent(bound, startSeq, "start", string(args), session, p.Key); e != nil {
				return "", e
			}
			ts.status = statusActive
			ts.owner = session
			ts.updatedSeq = startSeq
			ts.updatedTs = nowRFC3339()
			if worker {
				note = "'" + id + "' auto-started and submitted for review"
			} else {
				note = "'" + id + "' auto-started and completed"
			}
		}
		seq := f.nextSeq()
		if e := appendEvent(bound, seq, "complete", string(args), session, p.Key); e != nil {
			return "", e
		}
		ts.status = statusReview
		ts.owner = ""
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if !worker {
			acceptSeq := f.nextSeq()
			if e := appendEvent(bound, acceptSeq, "accept", string(args), session, p.Key); e != nil {
				return "", e
			}
			ts.status = statusDone
			ts.updatedSeq = acceptSeq
			ts.updatedTs = nowRFC3339()
		}
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, note), foot), nil
	})
}

func Fail(ctx context.Context, db store.DB, p Project, id, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		var voice string
		switch ts.status {
		case statusPending:
			voice = "'" + id + "' is pending; start it first"
		case statusReview:
			voice = "'" + id + "' is in review; accept or reject it first"
		case statusDone:
			voice = "'" + id + "' is done; read-only"
		case statusFailed:
			voice = "'" + id + "' is already failed"
		}
		if voice != "" {
			return "", fmt.Errorf("%s", voice)
		}
		released := ""
		if ts.owner != "" && ts.owner != session {
			released = ts.owner
		}
		note := "'" + id + "' failed"
		if released != "" {
			note += " (released from " + released + ")"
		}
		args, _ := json.Marshal(map[string]any{"id": id})
		seq := f.nextSeq()
		if e := appendEvent(bound, seq, "fail", string(args), session, p.Key); e != nil {
			return "", e
		}
		ts.status = statusFailed
		ts.owner = ""
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, note), foot), nil
	})
}

func Retry(ctx context.Context, db store.DB, p Project, id, session string) (string, error) {
	return verb(ctx, db, p, session, id, func(f *folded, ts *taskState) (ok bool, voice string) {
		if ts.status == statusFailed {
			return true, ""
		}
		return false, "'" + id + "' is not failed; nothing to retry"
	}, "retry", statusPending, "'"+id+"' back to pending")
}

// MaxNoteLen bounds one note's text: a note rides the event log and the
// compact snapshot, so an unbounded note is an unbounded log row.
const MaxNoteLen = 1000

// Claim takes the first task the caller may work: by default the first
// pending task whose dependency is done (the same order `next` shows),
// or with status="review" the first task in review that no reviewer
// holds. Either way the task becomes active for this session: pending
// moves to in_progress with the owner, a review task keeps its status
// and gains the holder. The answer is the taken id's echo, or "nothing
// to do" when no task qualifies.
func Claim(ctx context.Context, db store.DB, p Project, session, status string) (string, error) {
	if session == "" {
		session = anon
	}
	if status != "" && status != statusReview {
		return "", fmt.Errorf("todo: unknown claim status %q (only %s)", status, statusReview)
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		var ts *taskState
		for _, ot := range orderedTaskStates(f) {
			if status == statusReview {
				if ot.status == statusReview && ot.owner == "" {
					ts = ot
					break
				}
				continue
			}
			if ot.status == statusPending && blockedBy(f, ot) == "" {
				ts = ot
				break
			}
		}
		if ts == nil {
			return withFoot("nothing to do", foot), nil
		}
		args, _ := json.Marshal(map[string]any{"id": ts.id})
		seq := f.nextSeq()
		if e := appendEvent(bound, seq, "claim", string(args), session, p.Key); e != nil {
			return "", e
		}
		note := "'" + ts.id + "' claimed"
		if ts.status == statusPending {
			ts.status = statusActive
		} else {
			note = "'" + ts.id + "' claimed for review"
		}
		ts.owner = session
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, ts.id, note), foot), nil
	})
}

// Note appends a note to a task, whatever its status or owner: notes are
// how agents talk about shared work, so they do not need the hold. The
// note event rides the log and is replayable; the task's state does not
// move. The note must name a task and be non-empty; an overlong note is
// refused because it would bloat the log and the snapshot.
func Note(ctx context.Context, db store.DB, p Project, id, text, session string) (string, error) {
	if session == "" {
		session = anon
	}
	note, err := cleanNote(text, "note")
	if err != nil {
		return "", err
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		if _, ok := f.tasks[id]; !ok {
			return "", unknownTask(p, id)
		}
		args, _ := json.Marshal(map[string]any{"id": id, "note": note})
		seq := f.nextSeq()
		if e := appendEvent(bound, seq, "note", string(args), session, p.Key); e != nil {
			return "", e
		}
		f.tasks[id].notes = append(f.tasks[id].notes, noteState{text: note, session: session})
		return withFoot(echoTask(f, session, id, "note added to '"+id+"'"), foot), nil
	})
}

func cleanNote(text, verb string) (string, error) {
	note := strings.Join(strings.Fields(text), " ")
	if note == "" {
		if verb == "reject" {
			return "", fmt.Errorf("todo: reject requires a reason")
		}
		return "", fmt.Errorf("todo: note must not be empty")
	}
	if len(note) > MaxNoteLen {
		return "", fmt.Errorf("todo: %s is too long (%d chars; max %d)", verb, len(note), MaxNoteLen)
	}
	return note, nil
}

// Accept moves a task in review to done. The caller must not be a foreign
// holder: an unowned review task is auto-claimed (claim+accept, the same
// idiom as complete auto-starting a pending one), so a parent reviews its
// workers by read then accept, with no claim step.
func Accept(ctx context.Context, db store.DB, p Project, id, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		if e := reviewHold(ts, id, session); e != nil {
			return "", e
		}
		args, _ := json.Marshal(map[string]any{"id": id})
		note := "'" + id + "' accepted"
		if ts.owner == "" {
			claimSeq := f.nextSeq()
			if e := appendEvent(bound, claimSeq, "claim", string(args), session, p.Key); e != nil {
				return "", e
			}
			ts.owner = session
			ts.updatedSeq = claimSeq
			ts.updatedTs = nowRFC3339()
			note = "'" + id + "' auto-claimed and accepted"
		}
		seq := f.nextSeq()
		if e := appendEvent(bound, seq, "accept", string(args), session, p.Key); e != nil {
			return "", e
		}
		ts.status = statusDone
		ts.owner = ""
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, note), foot), nil
	})
}

// Reject sends a task in review back to pending and records the reason as
// a note, so the next worker sees why it bounced. An unowned review task
// is auto-claimed (claim+reject); a foreign holder still refuses. The
// reason is required and rides the reject event.
func Reject(ctx context.Context, db store.DB, p Project, id, reason, session string) (string, error) {
	if session == "" {
		session = anon
	}
	note, err := cleanNote(reason, "reject")
	if err != nil {
		return "", err
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		if e := reviewHold(ts, id, session); e != nil {
			return "", e
		}
		args, _ := json.Marshal(map[string]any{"id": id, "note": note})
		reply := "'" + id + "' rejected; reason noted"
		if ts.owner == "" {
			claimSeq := f.nextSeq()
			if e := appendEvent(bound, claimSeq, "claim", string(args), session, p.Key); e != nil {
				return "", e
			}
			ts.owner = session
			ts.updatedSeq = claimSeq
			ts.updatedTs = nowRFC3339()
			reply = "'" + id + "' auto-claimed and rejected; reason noted"
		}
		seq := f.nextSeq()
		if e := appendEvent(bound, seq, "reject", string(args), session, p.Key); e != nil {
			return "", e
		}
		ts.status = statusPending
		ts.owner = ""
		ts.notes = append(ts.notes, noteState{text: note, session: session})
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, reply), foot), nil
	})
}

func reviewHold(ts *taskState, id, session string) error {
	switch ts.status {
	case statusPending:
		return fmt.Errorf("'%s' is pending; not in review", id)
	case statusActive:
		return fmt.Errorf("'%s' is in progress; complete it first", id)
	case statusDone:
		return fmt.Errorf("'%s' is done; read-only", id)
	case statusFailed:
		return fmt.Errorf("'%s' failed; retry it first", id)
	}
	if ts.owner != "" && ts.owner != session {
		return fmt.Errorf("'%s' is claimed for review by %s", id, ts.owner)
	}
	return nil
}

func Move(ctx context.Context, db store.DB, p Project, id string, pos int, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		if pos < 1 || pos > len(f.tasks) {
			return "", fmt.Errorf("move position for '%s' must be between 1 and %d, got %d", id, len(f.tasks), pos)
		}
		args, _ := json.Marshal(map[string]any{"id": id, "pos": pos})
		seq := f.nextSeq()
		if e := appendEvent(bound, seq, "move", string(args), session, p.Key); e != nil {
			return "", e
		}
		if appliedMove(f, ts, pos) {
			ts.updatedSeq = seq
			ts.updatedTs = nowRFC3339()
		}
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, "'"+id+"' moved to position "+strconv.Itoa(pos)), foot), nil
	})
}

// Release returns a claimed task to pending, clearing the owner. It
// refuses the caller's own claim, an unclaimed task, a finished task,
// and a foreign claim that is still fresh (the owner's last event on
// the task is younger than StaleClaimAfter): a live session's work is
// not stolen by a tool call. The ended-session arm is Reap's, not
// Release's — the tool has no view of the session store.
func Release(ctx context.Context, db store.DB, p Project, id, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		switch ts.status {
		case statusDone:
			return "", fmt.Errorf("'%s' is done; read-only", id)
		case statusFailed:
			return "", fmt.Errorf("'%s' failed; retry it first", id)
		case statusPending:
			return "", fmt.Errorf("'%s' is not claimed; start it first", id)
		}
		review := ts.status == statusReview
		if ts.owner == session {
			if review {
				return "", fmt.Errorf("'%s' is claimed for review by you; accept or reject it", id)
			}
			return "", fmt.Errorf("'%s' is claimed by you; complete or fail it", id)
		}
		owner := ts.owner
		if owner == "" {
			if review {
				return "", fmt.Errorf("'%s' is in review and not claimed for review", id)
			}
			return "", fmt.Errorf("'%s' is not claimed; start it first", id)
		}
		if !staleClaim(ts, time.Now()) {
			if review {
				return "", fmt.Errorf("'%s' is claimed for review by %s (fresh); a live claim is not released", id, owner)
			}
			return "", fmt.Errorf("'%s' is claimed by %s (fresh); a live claim is not released — fail it first to take over", id, owner)
		}
		args, _ := json.Marshal(map[string]any{"id": id})
		seq := f.nextSeq()
		if e := appendEvent(bound, seq, "release", string(args), session, p.Key); e != nil {
			return "", e
		}
		if review {
			ts.owner = ""
			ts.updatedSeq = seq
			ts.updatedTs = nowRFC3339()
			if e := rewrite(tx, f, p.Key); e != nil {
				return "", e
			}
			return withFoot(echoTask(f, session, id, "'"+id+"' released (was claimed for review by "+owner+")"), foot), nil
		}
		ts.status = statusPending
		ts.owner = ""
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, "'"+id+"' released (was claimed by "+owner+")"), foot), nil
	})
}

// Reap releases every foreign claim in the project that is dead on
// arrival: an owner named in ended (a session row that closed) or a
// claim whose owner's last event on the task is older than
// StaleClaimAfter (a SIGKILL'd session leaves no row to consult). The
// caller's own claims are never touched. The note names each released
// task and the owner it was freed from; an idle reap returns "".
func Reap(ctx context.Context, db store.DB, p Project, ended []string, session string) (string, error) {
	if session == "" {
		session = anon
	}
	dead := map[string]bool{}
	for _, id := range ended {
		dead[id] = true
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		now := time.Now()
		released := []string{}
		for id, ts := range f.tasks {
			if (ts.status != statusActive && ts.status != statusReview) || ts.owner == "" || ts.owner == session {
				continue
			}
			if !dead[ts.owner] && !staleClaim(ts, now) {
				continue
			}
			owner := ts.owner
			args, _ := json.Marshal(map[string]any{"id": id})
			seq := f.nextSeq()
			if e := appendEvent(bound, seq, "release", string(args), session, p.Key); e != nil {
				return "", e
			}
			claimed := "was claimed by "
			if ts.status == statusReview {
				claimed = "was claimed for review by "
			} else {
				ts.status = statusPending
			}
			ts.owner = ""
			ts.updatedSeq = seq
			ts.updatedTs = nowRFC3339()
			released = append(released, id+" ("+claimed+owner+")")
		}
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		if len(released) == 0 {
			return withFoot("", foot), nil
		}
		sort.Strings(released)
		note := "released " + strconv.Itoa(len(released)) + " dead claim" + claimPlural(len(released)) + ": " + strings.Join(released, ", ")
		return withFoot("\u2192 "+note, foot), nil
	})
}

// Prune drops the done rows from the projection. The log keeps every
// event: prune is itself an event, so a replay drops the same rows and
// the queue's history stays reconstructable. Failed rows stay, they
// still ask for a retry. Without this door a long-lived queue's summary
// counts work that finished weeks ago, and a shared bucket inherits
// every past session's finished list. An idle prune appends nothing.
func Prune(ctx context.Context, db store.DB, p Project, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		n := 0
		for _, ts := range f.tasks {
			if ts.status == statusDone {
				n++
			}
		}
		if n == 0 {
			return withFoot(replyText(f, session, "nothing to prune (no done tasks)", false, p.Label), foot), nil
		}
		seq := f.nextSeq()
		args, _ := json.Marshal(map[string]any{"done": n})
		if e := appendEvent(bound, seq, "prune", string(args), session, p.Key); e != nil {
			return "", e
		}
		f.applyPrune()
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		note := "pruned " + strconv.Itoa(n) + " done task" + claimPlural(n)
		return withFoot(replyText(f, session, note, false, p.Label), foot), nil
	})
}

func (f *folded) applyPrune() {
	for id, ts := range f.tasks {
		if ts.status == statusDone {
			delete(f.tasks, id)
		}
	}
}

func staleClaim(ts *taskState, now time.Time) bool {
	t, err := time.Parse(time.RFC3339, ts.updatedTs)
	if err != nil {
		return false
	}
	return now.Sub(t) > StaleClaimAfter
}

func claimPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func Read(ctx context.Context, db store.DB, p Project, session string) (string, error) {
	return read(ctx, db, p, session, false)
}

func ReadAll(ctx context.Context, db store.DB, p Project, session string) (string, error) {
	return read(ctx, db, p, session, true)
}

func read(ctx context.Context, db store.DB, p Project, session string, all bool) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return replyText(f, session, "", all, p.Label), nil
	})
}

func verb(
	ctx context.Context,
	db store.DB,
	p Project,
	session, id string,
	check func(f *folded, ts *taskState) (ok bool, voice string),
	op, toStatus, note string,
) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		ok, voice := check(f, ts)
		if !ok {
			return "", fmt.Errorf("%s", voice)
		}
		args, _ := json.Marshal(map[string]any{"id": id})
		seq := f.nextSeq()
		if e := appendEvent(bound, seq, op, string(args), session, p.Key); e != nil {
			return "", e
		}
		ts.status = toStatus
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if toStatus == statusActive {
			if session == "" {
				ts.owner = anon
			} else {
				ts.owner = session
			}
		} else {
			ts.owner = ""
		}
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, note), foot), nil
	})
}

func replyText(f *folded, session, note string, all bool, label string) string {
	var b strings.Builder
	if note != "" {
		fmt.Fprintf(&b, "\u2192 %s\n", note)
	}
	b.WriteString(renderQueue(f, session, all, label))
	if foot := staleFooter(f); foot != "" {
		b.WriteString("\n" + foot)
	}
	return b.String()
}

const (
	STALE_THRESHOLD_SEQ      = 200
	COMPACT_THRESHOLD_EVENTS = 1000
)

func maybeCompact(bound context.Context, tx *sql.Tx, f *folded, session, scope string) (string, error) {
	if f.maxSeq-f.compactSeq < COMPACT_THRESHOLD_EVENTS {
		return "", nil
	}
	folded := f.maxSeq - f.compactSeq
	// The snapshot replaces the events the counters were rebuilt from, so
	// it carries them: ids are minted from the high-water mark, and the
	// create events that advanced it are about to be deleted. Forget it and
	// the next mint reissues an id some session still holds.
	args, _ := json.Marshal(map[string]any{
		"tasks": snapshotOf(f), "maxId": f.maxIdNum, "maxPos": f.maxPos,
	})
	seq := f.nextSeq()
	if e := appendEvent(bound, seq, "compact", string(args), session, scope); e != nil {
		return "", e
	}
	f.compactSeq = seq
	f.maxSeq = seq
	tsStr := nowRFC3339()
	for _, ts := range f.tasks {
		ts.createdSeq, ts.updatedSeq, ts.updatedTs = seq, seq, tsStr
	}
	if _, e := tx.Exec("DELETE FROM events WHERE scope = ? AND seq < ?", scope, seq); e != nil {
		return "", fmt.Errorf("todo: compact: %w", e)
	}
	return fmt.Sprintf("\u00b7 log compacted (%d events folded into the snapshot)", folded), nil
}

func snapshotOf(f *folded) []any {
	var out []any
	for _, ts := range f.tasks {
		link := any(nil)
		if ts.dep != "" {
			link = ts.dep
		}
		owner := any(nil)
		if ts.owner != "" {
			owner = ts.owner
		}
		m := map[string]any{
			"id": ts.id, "text": ts.text, "status": ts.status,
			"pos": ts.pos - 1, "dependsOn": link, "owner": owner,
			"updatedTs": ts.updatedTs,
		}
		if len(ts.notes) != 0 {
			notes := []any{}
			for _, n := range ts.notes {
				notes = append(notes, map[string]any{"note": n.text, "session": n.session})
			}
			m["notes"] = notes
		}
		out = append(out, m)
	}
	return out
}

func appliedMove(f *folded, ts *taskState, pos int) bool {
	if pos < 1 || pos > len(f.tasks) {
		return false
	}
	ordered := orderedTaskStates(f)
	idx := -1
	for i, ot := range ordered {
		if ot == ts {
			idx = i
			break
		}
	}
	if idx == -1 || idx == pos-1 {
		return false
	}
	removed := make([]*taskState, 0, len(ordered))
	removed = append(removed, ordered[:idx]...)
	removed = append(removed, ordered[idx+1:]...)
	rest := make([]*taskState, 0, len(removed)+1)
	rest = append(rest, removed[:pos-1]...)
	rest = append(rest, ts)
	rest = append(rest, removed[pos-1:]...)
	for i, ot := range rest {
		ot.pos = i + 1
	}
	return true
}

func (f *folded) applyMoveEvent(e eventRow) {
	var payload struct {
		ID  string `json:"id"`
		Pos int    `json:"pos"`
	}
	if json.Unmarshal([]byte(e.args), &payload) != nil || payload.ID == "" {
		return
	}
	ts, ok := f.tasks[payload.ID]
	if !ok {
		return
	}
	if !appliedMove(f, ts, payload.Pos) {
		return
	}
	ts.updatedSeq = e.seq
	ts.updatedTs = e.ts
}

func (f *folded) applyCompactEvent(e eventRow) {
	tasks := map[string]*taskState{}
	var payload struct {
		MaxID  int `json:"maxId"`
		MaxPos int `json:"maxPos"`
		Tasks  []struct {
			ID        string  `json:"id"`
			Text      string  `json:"text"`
			Status    string  `json:"status"`
			DependsOn *string `json:"dependsOn"`
			Pos       int     `json:"pos"`
			Owner     string  `json:"owner"`
			UpdatedTs string  `json:"updatedTs"`
			Notes     []struct {
				Note    string `json:"note"`
				Session string `json:"session"`
			} `json:"notes"`
		} `json:"tasks"`
	}
	if json.Unmarshal([]byte(e.args), &payload) == nil {
		for _, r := range payload.Tasks {
			if r.ID == "" || r.Text == "" {
				continue
			}
			status := r.Status
			switch status {
			case statusPending, statusActive, statusReview, statusDone, statusFailed:
			default:
				status = statusPending
			}
			pos := r.Pos + 1
			if pos < 1 {
				pos = 1
			}
			var dep string
			if r.DependsOn != nil {
				dep = *r.DependsOn
			}
			updatedTs := r.UpdatedTs
			if updatedTs == "" {
				updatedTs = e.ts
			}
			var notes []noteState
			for _, n := range r.Notes {
				if n.Note != "" {
					notes = append(notes, noteState{text: n.Note, session: n.Session})
				}
			}
			tasks[r.ID] = &taskState{
				id: r.ID, text: r.Text, status: status,
				pos: pos, dep: dep, owner: r.Owner, updatedTs: updatedTs, notes: notes,
			}
		}
		for _, ts := range tasks {
			if ts.dep != "" && tasks[ts.dep] == nil {
				ts.dep = ""
			}
		}
	}
	for _, ts := range tasks {
		ts.createdSeq = e.seq
		ts.updatedSeq = e.seq
	}
	// A snapshot written before the counters existed reports neither, and
	// 0 means "keep minting from what is here": the mint skips ids the
	// snapshot still holds, which is the pre-counter behaviour.
	if payload.MaxID > f.maxIdNum {
		f.maxIdNum = payload.MaxID
	}
	if payload.MaxPos > f.maxPos {
		f.maxPos = payload.MaxPos
	}
	f.tasks = tasks
	f.compactSeq = e.seq
}

// unknownTask names the queue it looked in: ids are per scope, so an id
// copied from another project's reply is the likeliest reason it is
// missing here, and the refusal should say where it failed to match.
func unknownTask(p Project, id string) error {
	where := p.Label
	if where == "" {
		where = "this queue"
	}
	if p.OutsideRepo {
		where += " (not a repo)"
	}
	return fmt.Errorf("no task '%s' in %s (ids are minted by the tool; copy from a reply)", id, where)
}

func withFoot(reply, foot string) string {
	if foot == "" {
		return reply
	}
	return reply + "\n" + foot
}

func mutate(ctx context.Context, db store.DB, p Project, act func(bound context.Context, tx *sql.Tx, f *folded) (string, error)) (string, error) {
	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx, p.Key)
	if err != nil {
		return "", err
	}
	f.label, f.notRepo = p.Label, p.OutsideRepo
	reply, err := act(bound, tx, f)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return reply, nil
}

func eventsOf(tx *sql.Tx, scope string) (*folded, error) {
	rows, err := tx.Query("SELECT seq, op, args, session, ts, scope FROM events ORDER BY seq")
	if err != nil {
		return nil, fmt.Errorf("todo: event log: %w", err)
	}
	defer rows.Close()
	f := newFolded()
	for rows.Next() {
		var e eventRow
		var session sql.NullString
		if err := rows.Scan(&e.seq, &e.op, &e.args, &session, &e.ts, &e.scope); err != nil {
			return nil, fmt.Errorf("todo: event log: %w", err)
		}
		e.session = session.String
		f.globalSeq = e.seq
		if e.scope != scope {
			continue
		}
		f.apply(e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("todo: event log: %w", err)
	}
	return f, nil
}

func appendEvent(bound context.Context, seq int64, op, args, session, scope string) error {
	if session == "" {
		session = anon
	}
	s := session
	sess := &s
	_, err := tododomain.NewEventDomain().InsertEvent(bound, tododomain.Event{
		Seq: seq, Ts: nowRFC3339(), Op: op, Args: args, Session: sess, Scope: scope,
	})
	if err != nil {
		return fmt.Errorf("todo: event append: %w", err)
	}
	return nil
}

func rewrite(tx *sql.Tx, f *folded, scope string) error {
	if _, err := tx.Exec("DELETE FROM task_deps WHERE scope = ?", scope); err != nil {
		return fmt.Errorf("todo: rewrite: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM tasks WHERE scope = ?", scope); err != nil {
		return fmt.Errorf("todo: rewrite: %w", err)
	}
	var order []*taskState
	for _, ts := range f.tasks {
		order = append(order, ts)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].pos != order[j].pos {
			return order[i].pos < order[j].pos
		}
		return order[i].createdSeq < order[j].createdSeq
	})
	for _, ts := range order {
		var dep *string
		if ts.dep != "" {
			d := ts.dep
			dep = &d
		}
		_, err := tx.Exec(
			"INSERT INTO tasks (scope, id, text, status, pos, created_seq, updated_seq) VALUES (?, ?, ?, ?, ?, ?, ?)",
			scope, ts.id, ts.text, ts.status, ts.pos, ts.createdSeq, ts.updatedSeq,
		)
		if err != nil {
			return fmt.Errorf("todo: rewrite: %w", err)
		}
		if dep != nil {
			_, err := tx.Exec(
				"INSERT INTO task_deps (scope, task_id, depends_on, created_seq) VALUES (?, ?, ?, ?)",
				scope, ts.id, ts.dep, ts.updatedSeq,
			)
			if err != nil {
				return fmt.Errorf("todo: rewrite: %w", err)
			}
		}
	}
	return nil
}

func asGiven(items []CreateItem) []any {
	var out []any
	for _, it := range items {
		m := map[string]any{"text": it.Text}
		switch {
		case it.DependsOn != nil:
			m["dependsOn"] = *it.DependsOn
		case it.DepNull:
			m["dependsOn"] = nil
		}
		out = append(out, m)
	}
	return out
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
