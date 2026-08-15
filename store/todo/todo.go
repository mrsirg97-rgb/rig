// Package todo is the task-queue store: pane's semantics, Go over the
// generated substrate. SPEC_STATE's "### todo" section is the spec; pane's
// TODO_SPEC rev 2 and TASK_TREE_SPEC carry the voice and the named cases.
//
// The event log is the spine. tasks/task_deps are a disposable projection,
// rebuilt from the log inside every transaction and never trusted. Replay
// is total: malformed or inapplicable rows are skipped, never thrown.
// Positions are minted, never mutated in place; moves are events. Create
// is the only dependency mutation point; the DAG is validated there, at the
// boundary, and refused loudly with the problem in pane's teaching voice.
package todo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/looper/store"
	tododomain "github.com/mrsirg97-rgb/looper/store/todo/domain"
)

// SchemaVersion is applied at Open; mismatches are refused loudly.
const SchemaVersion = 1

const anon = "anon"

// CreateItem is one entry of a create payload. DependsOn is id, exact text,
// null (clears), or omitted (keeps) — pane's TASK_TREE contract.
type CreateItem struct {
	Text      string
	DependsOn *string
	DepNull   bool // the payload carried an explicit null (clears the link)
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

// --- fold (the pure replay) ---

type taskState struct {
	id         string
	text       string
	status     string
	pos        int
	dep        string
	owner      string
	createdSeq int64
	updatedSeq int64
}

type folded struct {
	tasks    map[string]*taskState
	maxSeq   int64
	maxPos   int
	maxIdNum int
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
}

func attrOf(e eventRow) string {
	if e.session == "" {
		return anon
	}
	return e.session
}

// apply folds one event into the state. Total: malformed rows and
// inapplicable transitions are skipped, never thrown.
func (f *folded) apply(e eventRow) {
	if e.seq > f.maxSeq {
		f.maxSeq = e.seq
	}
	switch e.op {
	case "create":
		f.applyCreate(e)
	case "start", "complete", "fail", "retry":
		f.applyVerb(e)
	case "move", "compact":
		// TD3b: renumbering and snapshot replay land here.
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
		return // total: one bad row cannot invalidate the queue
	}
	if len(items) == 0 {
		f.tasks = map[string]*taskState{} // clear: the only destructive verb
		return
	}
	type pendingRef struct {
		ts    *taskState
		clear bool
		ref   string
	}
	var refs []pendingRef
	seen := map[string]bool{}
	for _, item := range items {
		if item.text == "" || seen[item.text] {
			continue // first occurrence wins
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
			createdSeq: e.seq, updatedSeq: e.seq,
		}
		ts.id = f.mintID()
		ts.pos = f.nextPos()
		f.tasks[ts.id] = ts
		if item.hasDep {
			refs = append(refs, pendingRef{ts: ts, clear: item.depNull, ref: item.dep})
		}
	}
	// Dependencies resolve now that every batch-internal text carries an
	// id. Unresolvable references drop: total replay, never a deadlocked
	// task. Explicit null clears.
	for _, pr := range refs {
		if pr.clear {
			pr.ts.dep = ""
			continue
		}
		if ref := resolveRef(f, pr.ref); ref != "" {
			pr.ts.dep = ref
		}
	}
}

// resolveRef: id against existing tasks, exact text batch-internal or
// existing. Batch-internal by text only — a minted id cannot be known in
// advance, and id-first against the fold would shadow a task whose text
// looks like an id.
func resolveRef(f *folded, raw string) string {
	if ts, ok := f.tasks[raw]; ok && ts.status != "" && !looksLikeBatchText(f, raw) {
		return raw
	}
	if ts := f.byText(raw); ts != nil {
		return ts.id
	}
	return ""
}

func looksLikeBatchText(f *folded, raw string) bool {
	// conservative: an id-shaped value that names no task falls through to
	// the text rule, which is how batch-internal links land.
	_, ok := f.tasks[raw]
	return !ok
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
}

// --- planning (create, at the boundary) ---

// planCreate dedups, mints ids, resolves dependencies against existing
// tasks plus the batch, and collects problems — never throws. Any problem
// rejects the whole batch: nothing is created, no event appended.
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
	for _, item := range items {
		raw := item.raw()
		if raw.text == "" || seen[raw.text] {
			continue // first occurrence wins; duplicates ignored entirely
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
		f.tasks[ts.id] = ts // lands tentatively; rolled back if problems
		modified = append(modified, ts)
		if raw.hasDep {
			refs = append(refs, depRef{ts: ts, text: raw.text, depNull: raw.depNull, dep: raw.dep})
		}
	}
	// Dependencies resolve now that every batch-internal text carries an id
	// — forward references included.
	for _, dr := range refs {
		if dr.depNull {
			dr.ts.dep = "" // explicit null clears
			continue
		}
		if dr.dep == dr.text {
			addOnce(&problems, fmt.Sprintf("'%s' cannot depend on itself", dr.text))
			continue
		}
		if resolved := resolveRef(f, dr.dep); resolved != "" {
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

// cyclePath: DFS over existing links plus the planned ones; the first cycle
// as its node ids, or nil when acyclic.
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

// --- render ---

const (
	statusPending = "pending"
	statusActive  = "in_progress"
	statusDone    = "done"
	statusFailed  = "failed"
)

func marker(status string) string {
	switch status {
	case statusDone, statusFailed:
		return "[x]"
	case statusActive:
		return "[~]"
	default:
		return "[ ]"
	}
}

func render(f *folded) string {
	var order []string
	for id := range f.tasks {
		order = append(order, id)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := f.tasks[order[i]], f.tasks[order[j]]
		if a.pos != b.pos {
			return a.pos < b.pos
		}
		return a.createdSeq < b.createdSeq
	})
	done := 0
	for _, id := range order {
		if st := f.tasks[id].status; st == statusDone || st == statusFailed {
			done++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d/%d done", done, len(order))
	var lines []string
	for _, id := range order {
		ts := f.tasks[id]
		line := fmt.Sprintf("%s %s %s", ts.id, marker(ts.status), ts.text)
		if ts.dep != "" && ts.status != statusDone && ts.status != statusFailed {
			if dep := f.tasks[ts.dep]; dep != nil && dep.status != statusDone {
				line += fmt.Sprintf(" \u00b7 waits on %s", ts.dep)
			}
		}
		lines = append(lines, line)
	}
	if len(lines) != 0 {
		b.WriteString("\n" + strings.Join(lines, "\n"))
	}
	return b.String()
}

// --- verbs (the transactional surface) ---

// Create lands the payload: fold, plan at the boundary, refuse loudly with
// the problem when planning finds one, otherwise append the event as given
// and persist the projection. One transaction, serializable.
func Create(ctx context.Context, db store.DB, items []CreateItem, session string) (string, error) {
	return mutate(ctx, db, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		modified, problems := planCreate(f, items)
		if len(problems) != 0 {
			sort.Strings(problems)
			return "", fmt.Errorf("todo: %s", strings.Join(problems, "; "))
		}
		if len(items) == 0 {
			f.tasks = map[string]*taskState{} // clear: the only destructive verb
		}
		args, _ := json.Marshal(map[string]any{"tasks": asGiven(items)})
		seq, e := appendEvent(bound, f.maxSeq+1, "create", string(args), session)
		if e != nil {
			return "", e
		}
		for _, ts := range modified {
			ts.updatedSeq = seq
		}
		if e := rewrite(tx, f); e != nil {
			return "", e
		}
		return render(f), nil
	})
}

// Start/Complete/Fail/Retry: pane's FSM. Claim checks and completion gating
// land in TD3b; the transitions and the voice are pane's now.
func Start(ctx context.Context, db store.DB, id, session string) (string, error) {
	return verb(ctx, db, session, id, func(ts *taskState) (ok bool, voice string) {
		switch ts.status {
		case statusPending:
			return true, ""
		case statusActive:
			return false, "'" + id + "' is already in_progress"
		case statusDone:
			return false, "'" + id + "' is done (read-only)"
		default:
			return false, "'" + id + "' is failed; retry it first"
		}
	}, "start", "in_progress")
}

func Complete(ctx context.Context, db store.DB, id, session string) (string, error) {
	return verb(ctx, db, session, id, func(ts *taskState) (ok bool, voice string) {
		switch ts.status {
		case statusActive:
			return true, ""
		case statusPending:
			return false, "'" + id + "' is pending; start it first"
		case statusDone:
			return false, "'" + id + "' is done (read-only)"
		default:
			return false, "'" + id + "' is failed"
		}
	}, "complete", "done")
}

func Fail(ctx context.Context, db store.DB, id, session string) (string, error) {
	return verb(ctx, db, session, id, func(ts *taskState) (ok bool, voice string) {
		switch ts.status {
		case statusActive:
			return true, ""
		case statusPending:
			return false, "'" + id + "' is pending; start it first"
		case statusDone:
			return false, "'" + id + "' is done (read-only)"
		default:
			return false, "'" + id + "' is already failed"
		}
	}, "fail", "failed")
}

func Retry(ctx context.Context, db store.DB, id, session string) (string, error) {
	return verb(ctx, db, session, id, func(ts *taskState) (ok bool, voice string) {
		if ts.status == statusFailed {
			return true, ""
		}
		return false, "'" + id + "' is not failed"
	}, "retry", "pending")
}

func Read(ctx context.Context, db store.DB) (string, error) {
	return mutate(ctx, db, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		if e := rewrite(tx, f); e != nil {
			return "", e
		}
		return render(f), nil
	})
}

func verb(
	ctx context.Context,
	db store.DB,
	session, id string,
	check func(*taskState) (ok bool, voice string),
	op, toStatus string,
) (string, error) {
	return mutate(ctx, db, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		ts, ok := f.tasks[id]
		if !ok {
			return "", fmt.Errorf("no such task '%s'", id)
		}
		ok, voice := check(ts)
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
		return render(f), nil
	})
}

// --- plumbing ---

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
	rows, err := tx.Query("SELECT seq, op, args, session FROM events ORDER BY seq")
	if err != nil {
		return nil, fmt.Errorf("todo: event log: %w", err)
	}
	defer rows.Close()
	f := newFolded()
	for rows.Next() {
		var e eventRow
		var session sql.NullString
		if err := rows.Scan(&e.seq, &e.op, &e.args, &session); err != nil {
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

// appendEvent lands one event through the generated domain and returns the
// minted seq. seq is omitted from the INSERT (rowid semantics): strictly
// increasing by construction, no mint query owned.
func appendEvent(bound context.Context, seq int64, op, args, session string) (int64, error) {
	var sess *string
	if session != "" {
		s := session
		sess = &s
	}
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
		if it.DependsOn != nil {
			m["dependsOn"] = *it.DependsOn
		}
		out = append(out, m)
	}
	return out
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
