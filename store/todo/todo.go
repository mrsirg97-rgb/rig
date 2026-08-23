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

const SchemaVersion = 1

func DDL() []string { return tododdl.Statements() }

func Statements() []string {
	out := tododdl.Statements()
	return append(out, todometa.ExtraStatements()...)
}

const anon = "anon"

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
}

type folded struct {
	compactSeq int64
	tasks      map[string]*taskState
	maxSeq     int64
	maxPos     int
	maxIdNum   int
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

type eventRow struct {
	seq     int64
	op      string
	args    string
	session string
	ts      string
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
	switch e.op {
	case "create":
		f.applyCreate(e)
	case "start", "complete", "fail", "retry":
		f.applyVerb(e)
	case "move":
		f.applyMoveEvent(e)
	case "compact":
		f.applyCompactEvent(e)
	}
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
			ts.status = "done"
			ts.owner = ""
		}
	case "fail":
		if ts.status == "in_progress" {
			ts.status = "failed"
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

func planCreate(f *folded, items []CreateItem) (modified []*taskState, problems []string) {
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
		ex := f.byText(raw.text)
		if ex != nil {
			planned[raw.text] = ex
			if raw.hasDep {
				refs = append(refs, depRef{ts: ex, text: raw.text, depNull: raw.depNull, dep: raw.dep})
			}
			continue
		}
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
	return modified, problems
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
	statusDone    = "done"
	statusFailed  = "failed"
)

func marker(status string) string {
	switch status {
	case statusDone:
		return "[x]"
	case statusFailed:
		return "[!]"
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
	if ts.status != statusActive || ts.owner == "" || ts.owner == session {
		return ""
	}
	owner := ts.owner
	if len(owner) > 8 {
		owner = owner[:8]
	}
	return " \u00b7 claimed by " + owner
}

func staleFooter(f *folded) string {
	if f.maxSeq <= STALE_THRESHOLD_SEQ {
		return ""
	}
	n := 0
	latest := ""
	for _, ts := range f.tasks {
		if ts.status != statusPending && ts.status != statusActive {
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
	done, failed := 0, 0
	nextID := ""
	for _, ts := range ordered {
		switch ts.status {
		case statusDone:
			done++
		case statusFailed:
			failed++
		}
	}
	for _, ts := range ordered {
		if ts.status == statusPending && blockedBy(f, ts) == "" {
			nextID = ts.id
			break
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d/%d done", done, len(ordered))
	if nextID != "" {
		fmt.Fprintf(&b, " \u00b7 next: %s", nextID)
	}
	if failed != 0 {
		fmt.Fprintf(&b, " \u00b7 %d failed", failed)
	}
	return b.String()
}

func lineOf(f *folded, ts *taskState, session string) string {
	line := fmt.Sprintf("  %s %s %s", ts.id, marker(ts.status), ts.text)
	if blocker := blockedBy(f, ts); blocker != "" {
		line += fmt.Sprintf(" \u00b7 waits on %s", blocker)
	}
	line += claimSuffix(ts, session)
	return line
}

func renderQueue(f *folded, session string, all bool) string {
	ordered := orderedTaskStates(f)
	if len(ordered) == 0 {
		return "(no tasks in this directory's queue)"
	}
	var b strings.Builder
	b.WriteString(summaryOf(f))
	for _, ts := range ordered {
		if !all && ts.status == statusDone {
			continue
		}
		b.WriteString("\n" + lineOf(f, ts, session))
	}
	return b.String()
}

func echoTask(f *folded, session, id, note string) string {
	var b strings.Builder
	if note != "" {
		fmt.Fprintf(&b, "\u2192 %s\n", note)
	}
	if ts := f.tasks[id]; ts != nil {
		b.WriteString(lineOf(f, ts, session))
	}
	b.WriteString("\n" + summaryOf(f))
	if foot := staleFooter(f); foot != "" {
		b.WriteString("\n" + foot)
	}
	return b.String()
}

func Create(ctx context.Context, db store.DB, items []CreateItem, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session)
		if e != nil {
			return "", e
		}
		modified, problems := planCreate(f, items)
		if len(problems) != 0 {
			sort.Strings(problems)
			return "", fmt.Errorf("todo: %s", strings.Join(problems, "; "))
		}
		note := "queue replaced with " + strconv.Itoa(len(items)) + " tasks"
		if len(items) == 0 {
			f.tasks = map[string]*taskState{}
			note = "queue cleared"
		}
		args, _ := json.Marshal(map[string]any{"tasks": asGiven(items)})
		seq, e := appendEvent(bound, f.maxSeq+1, "create", string(args), session)
		if e != nil {
			return "", e
		}
		for _, ts := range modified {
			ts.updatedSeq = seq
			ts.updatedTs = nowRFC3339()
		}
		if e := rewrite(tx, f); e != nil {
			return "", e
		}
		return withFoot(replyText(f, session, note, false), foot), nil
	})
}

func Start(ctx context.Context, db store.DB, id, session string) (string, error) {
	return verb(ctx, db, session, id, func(f *folded, ts *taskState) (ok bool, voice string) {
		switch ts.status {
		case statusPending:
			return true, ""
		case statusActive:
			voice := "'" + id + "' is already in progress"
			if ts.owner != "" {
				voice += " (claimed by " + ts.owner + ")"
			}
			return false, voice
		case statusDone:
			return false, "'" + id + "' is done; read-only"
		default:
			return false, "'" + id + "' failed; retry it first"
		}
	}, "start", statusActive, "'"+id+"' started")
}

func Complete(ctx context.Context, db store.DB, id, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(id)
		}
		switch ts.status {
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
		if ts.status == statusPending {
			startSeq, e := appendEvent(bound, f.maxSeq+1, "start", string(args), session)
			if e != nil {
				return "", e
			}
			ts.status = statusActive
			ts.owner = session
			ts.updatedSeq = startSeq
			ts.updatedTs = nowRFC3339()
			f.maxSeq = startSeq
			note = "'" + id + "' auto-started and completed"
		}
		seq, e := appendEvent(bound, f.maxSeq+1, "complete", string(args), session)
		if e != nil {
			return "", e
		}
		ts.status = statusDone
		ts.owner = ""
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if e := rewrite(tx, f); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, note), foot), nil
	})
}

func Fail(ctx context.Context, db store.DB, id, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(id)
		}
		var voice string
		switch ts.status {
		case statusPending:
			voice = "'" + id + "' is pending; start it first"
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
		seq, e := appendEvent(bound, f.maxSeq+1, "fail", string(args), session)
		if e != nil {
			return "", e
		}
		ts.status = statusFailed
		ts.owner = ""
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if e := rewrite(tx, f); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, note), foot), nil
	})
}

func Retry(ctx context.Context, db store.DB, id, session string) (string, error) {
	return verb(ctx, db, session, id, func(f *folded, ts *taskState) (ok bool, voice string) {
		if ts.status == statusFailed {
			return true, ""
		}
		return false, "'" + id + "' is not failed; nothing to retry"
	}, "retry", statusPending, "'"+id+"' back to pending")
}

func Move(ctx context.Context, db store.DB, id string, pos int, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(id)
		}
		if pos < 1 || pos > len(f.tasks) {
			return "", fmt.Errorf("move position for '%s' must be between 1 and %d, got %d", id, len(f.tasks), pos)
		}
		args, _ := json.Marshal(map[string]any{"id": id, "pos": pos})
		seq, e := appendEvent(bound, f.maxSeq+1, "move", string(args), session)
		if e != nil {
			return "", e
		}
		if appliedMove(f, ts, pos) {
			ts.updatedSeq = seq
			ts.updatedTs = nowRFC3339()
		}
		if e := rewrite(tx, f); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, "'"+id+"' moved to position "+strconv.Itoa(pos)), foot), nil
	})
}

func Read(ctx context.Context, db store.DB, session string) (string, error) {
	return read(ctx, db, session, false)
}

func ReadAll(ctx context.Context, db store.DB, session string) (string, error) {
	return read(ctx, db, session, true)
}

func read(ctx context.Context, db store.DB, session string, all bool) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		if e := rewrite(tx, f); e != nil {
			return "", e
		}
		return replyText(f, session, "", all), nil
	})
}

func verb(
	ctx context.Context,
	db store.DB,
	session, id string,
	check func(f *folded, ts *taskState) (ok bool, voice string),
	op, toStatus, note string,
) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(id)
		}
		ok, voice := check(f, ts)
		if !ok {
			return "", fmt.Errorf("%s", voice)
		}
		args, _ := json.Marshal(map[string]any{"id": id})
		seq, e := appendEvent(bound, f.maxSeq+1, op, string(args), session)
		if e != nil {
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
		if e := rewrite(tx, f); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, note), foot), nil
	})
}

func replyText(f *folded, session, note string, all bool) string {
	var b strings.Builder
	if note != "" {
		fmt.Fprintf(&b, "\u2192 %s\n", note)
	}
	b.WriteString(renderQueue(f, session, all))
	if foot := staleFooter(f); foot != "" {
		b.WriteString("\n" + foot)
	}
	return b.String()
}

const (
	STALE_THRESHOLD_SEQ      = 200
	COMPACT_THRESHOLD_EVENTS = 1000
)

func maybeCompact(bound context.Context, tx *sql.Tx, f *folded, session string) (string, error) {
	if f.maxSeq-f.compactSeq < COMPACT_THRESHOLD_EVENTS {
		return "", nil
	}
	folded := f.maxSeq - f.compactSeq
	args, _ := json.Marshal(map[string]any{"tasks": snapshotOf(f)})
	seq, e := appendEvent(bound, f.maxSeq+1, "compact", string(args), session)
	if e != nil {
		return "", e
	}
	f.compactSeq = seq
	f.maxSeq = seq
	tsStr := nowRFC3339()
	for _, ts := range f.tasks {
		ts.createdSeq, ts.updatedSeq, ts.updatedTs = seq, seq, tsStr
	}
	if _, e := tx.Exec("DELETE FROM events WHERE seq < ?", seq); e != nil {
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
		out = append(out, map[string]any{
			"id": ts.id, "text": ts.text, "status": ts.status,
			"pos": ts.pos - 1, "dependsOn": link, "owner": owner,
		})
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
		Tasks []struct {
			ID        string  `json:"id"`
			Text      string  `json:"text"`
			Status    string  `json:"status"`
			DependsOn *string `json:"dependsOn"`
			Pos       int     `json:"pos"`
			Owner     string  `json:"owner"`
		} `json:"tasks"`
	}
	if json.Unmarshal([]byte(e.args), &payload) == nil {
		for _, r := range payload.Tasks {
			if r.ID == "" || r.Text == "" {
				continue
			}
			status := r.Status
			switch status {
			case statusPending, statusActive, statusDone, statusFailed:
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
			tasks[r.ID] = &taskState{
				id: r.ID, text: r.Text, status: status,
				pos: pos, dep: dep, owner: r.Owner,
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
		ts.updatedTs = e.ts
	}
	f.tasks = tasks
	f.compactSeq = e.seq
}

func unknownTask(id string) error {
	return fmt.Errorf("no task '%s' (ids are minted by the tool; copy from a reply)", id)
}

func withFoot(reply, foot string) string {
	if foot == "" {
		return reply
	}
	return reply + "\n" + foot
}

func mutate(ctx context.Context, db store.DB, act func(bound context.Context, tx *sql.Tx, f *folded) (string, error)) (string, error) {
	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx)
	if err != nil {
		return "", err
	}
	reply, err := act(bound, tx, f)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return reply, nil
}

func eventsOf(tx *sql.Tx) (*folded, error) {
	rows, err := tx.Query("SELECT seq, op, args, session, ts FROM events ORDER BY seq")
	if err != nil {
		return nil, fmt.Errorf("todo: event log: %w", err)
	}
	defer rows.Close()
	f := newFolded()
	for rows.Next() {
		var e eventRow
		var session sql.NullString
		if err := rows.Scan(&e.seq, &e.op, &e.args, &session, &e.ts); err != nil {
			return nil, fmt.Errorf("todo: event log: %w", err)
		}
		e.session = session.String
		f.apply(e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("todo: event log: %w", err)
	}
	return f, nil
}

func appendEvent(bound context.Context, seq int64, op, args, session string) (int64, error) {
	if session == "" {
		session = anon
	}
	s := session
	sess := &s
	ev, err := tododomain.NewEventDomain().InsertEvent(bound, tododomain.Event{
		Seq: seq, Ts: nowRFC3339(), Op: op, Args: args, Session: sess,
	})
	if err != nil {
		return 0, fmt.Errorf("todo: event append: %w", err)
	}
	return ev.Seq, nil
}

func rewrite(tx *sql.Tx, f *folded) error {
	if _, err := tx.Exec("DELETE FROM task_deps"); err != nil {
		return fmt.Errorf("todo: rewrite: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM tasks"); err != nil {
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
			"INSERT INTO tasks (id, text, status, pos, created_seq, updated_seq) VALUES (?, ?, ?, ?, ?, ?)",
			ts.id, ts.text, ts.status, ts.pos, ts.createdSeq, ts.updatedSeq,
		)
		if err != nil {
			return fmt.Errorf("todo: rewrite: %w", err)
		}
		if dep != nil {
			_, err := tx.Exec(
				"INSERT INTO task_deps (task_id, depends_on, created_seq) VALUES (?, ?, ?)",
				ts.id, ts.dep, ts.updatedSeq,
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
