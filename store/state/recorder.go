package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state/domain"
)

type Recorder struct {
	inner    core.Frontend
	db       store.DB
	cwd      string
	model    string
	version  string
	sid       string
	session   *core.Session
	buffer    strings.Builder
	reason    strings.Builder
	pending   []core.ToolCall
	lastSeq   int64
	resultMap map[string][]string
	ensured   bool
	mu        sync.Mutex
	snapshot  func(*core.Session) map[string]core.FileState
}

func NewRecorder(inner core.Frontend, db store.DB, cwd, model, version, sid string, session *core.Session) *Recorder {
	return &Recorder{
		inner: inner, db: db, cwd: cwd, model: model,
		version: version, sid: sid, session: session,
		resultMap: map[string][]string{},
	}
}

// Snapshot wires the file tool's snapshot so the recorder can persist
// the session's recorded file states without racing the tools'
// concurrent records; the default (nil) skips the files upsert.
func (r *Recorder) Snapshot(fn func(*core.Session) map[string]core.FileState) *Recorder {
	r.snapshot = fn
	return r
}

func (r *Recorder) Input(ctx context.Context) (string, error) {
	if e := r.ensure(); e != nil {
		r.loud("session row", e)
	}

	_ = r.land()
	text, err := r.inner.Input(ctx)
	if err != nil {
		return text, err
	}
	seq, e := RecordMessage(ctx, r.db, r.sid, "user", text, nil, nil)
	if e != nil {
		r.loud("user message", e)
	} else {
		r.setLastSeq(seq)
	}
	r.upsertFiles()
	return text, err
}

func (r *Recorder) Notify(ev core.Event) {
	r.observe(ev)
	r.inner.Notify(ev)
}

func (r *Recorder) observe(ev core.Event) {
	if e := r.ensure(); e != nil {
		r.loud("session row", e)
	}
	switch e := ev.(type) {
	case core.TextDelta:
		r.mu.Lock()
		r.buffer.WriteString(e.Text)
		r.mu.Unlock()
	case core.ReasoningDelta:
		r.mu.Lock()
		r.reason.WriteString(e.Text)
		r.mu.Unlock()
	case core.ToolCallEvent:
		r.mu.Lock()
		r.pending = append(r.pending, e.Call)
		r.mu.Unlock()
	case core.ToolResult:

		if e := r.ensure(); e != nil {
			r.loud("session row", e)
		}
		var failure *string
		if e.Err != nil {
			f := e.Err.Error()
			failure = &f
		}
		storage, ok := r.popStorage(e.ID)
		if !ok {
			storage = e.ID
		}
		if e2 := RecordToolResult(context.Background(), r.db, r.sid, r.lastSeq, storage, e.Content, failure); e2 != nil {
			r.loud("tool result "+e.ID, e2)
		}
	case core.Done:
		seq := r.land()
		if seq > 0 {
			if e2 := RecordUsage(context.Background(), r.db, seq, int64(e.Usage.Prompt), int64(e.Usage.Completion), int64(e.Usage.CacheRead), int64(e.Usage.CacheWrite)); e2 != nil {
				r.loud("usage", e2)
			}
		}
		r.upsertFiles()
	case core.Compacted:

		r.landCompacted(e)
	case core.Fault:
		if _, e2 := RecordFault(context.Background(), r.db, r.sid, now(), e.Err.Error()); e2 != nil {
			r.loud("fault", e2)
		}
		r.discardPartial()
	case core.TurnEnd:

		r.discardPartial()
	}
}

func (r *Recorder) discardPartial() {
	r.mu.Lock()
	r.buffer.Reset()
	r.reason.Reset()
	r.pending = nil
	r.mu.Unlock()
}

func (r *Recorder) land() (seq int64) {
	r.mu.Lock()
	text := r.buffer.String()
	reason := r.reason.String()
	calls := r.pending
	r.buffer.Reset()
	r.reason.Reset()
	r.pending = nil
	r.mu.Unlock()
	if text == "" && len(calls) == 0 && reason == "" {
		return 0
	}
	var toolID *string
	if len(calls) == 1 {
		id := calls[0].ID
		toolID = &id
	}
	var reasoning *string
	if reason != "" {
		reasoning = &reason
	}
	var e error
	seq, e = RecordMessage(context.Background(), r.db, r.sid, "assistant", text, reasoning, toolID)
	if e != nil {
		r.loud("assistant message", e)
		return 0
	}
	r.setLastSeq(seq)
	r.mu.Lock()
	r.resultMap = map[string][]string{}
	used := map[string]int{}
	for _, call := range calls {
		n := used[call.ID]
		used[call.ID] = n + 1
		storage := call.ID
		if n > 0 {
			storage = fmt.Sprintf("%s-%d", call.ID, n+1)
		}
		if e2 := RecordToolCall(context.Background(), r.db, r.sid, seq, storage, call.Name, string(call.Args)); e2 != nil {
			r.loud("tool call "+call.ID, e2)
			continue
		}
		r.resultMap[call.ID] = append(r.resultMap[call.ID], storage)
	}
	r.mu.Unlock()
	return seq
}

func (r *Recorder) popStorage(id string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	queue := r.resultMap[id]
	if len(queue) == 0 {
		return "", false
	}
	storage := queue[0]
	if len(queue) == 1 {
		delete(r.resultMap, id)
	} else {
		r.resultMap[id] = queue[1:]
	}
	return storage, true
}

func (r *Recorder) landCompacted(ev core.Compacted) {
	seq, e := RecordMessage(context.Background(), r.db, r.sid, "user", ev.Summary, nil, nil)
	if e != nil {
		r.loud("summary row", e)
		return
	}
	r.setLastSeq(seq)
	if e2 := RecordUsage(context.Background(), r.db, seq, int64(ev.Usage.Prompt), int64(ev.Usage.Completion), int64(ev.Usage.CacheRead), int64(ev.Usage.CacheWrite)); e2 != nil {
		r.loud("summary usage", e2)
	}
	r.relandTail()
}

func (r *Recorder) relandTail() {
	if r.session == nil {
		return
	}
	r.mu.Lock()
	msgs := append([]core.Message(nil), r.session.Messages...)
	r.mu.Unlock()
	if len(msgs) <= 1 {
		return
	}
	tail := msgs[1:]
	counts := map[string]int{}
	for _, m := range tail {
		if m.Role == core.RoleAssistant {
			for _, call := range m.ToolCalls {
				counts[call.ID]++
			}
		}
	}
	seen := map[string]int{}
	for _, m := range tail {
		switch m.Role {
		case core.RoleUser:
			if seq, e := RecordMessage(context.Background(), r.db, r.sid, "user", m.Content, nil, nil); e != nil {
				r.loud("re-landed user", e)
			} else {
				r.setLastSeq(seq)
			}
		case core.RoleAssistant:
			var toolID *string
			if len(m.ToolCalls) == 1 {
				id := m.ToolCalls[0].ID
				toolID = &id
			}
			var reasoning *string
			if m.Reasoning != "" {
				reasoning = &m.Reasoning
			}
			seq, e := RecordMessage(context.Background(), r.db, r.sid, "assistant", m.Content, reasoning, toolID)
			if e != nil {
				r.loud("re-landed assistant", e)
				continue
			}
			r.setLastSeq(seq)
			r.mu.Lock()
			r.resultMap = map[string][]string{}
			used := map[string]int{}
			for _, call := range m.ToolCalls {
				n := used[call.ID]
				used[call.ID] = n + 1
				storage := call.ID
				if n > 0 {
					storage = fmt.Sprintf("%s-%d", call.ID, n+1)
				}
				if e2 := RecordToolCall(context.Background(), r.db, r.sid, seq, storage, call.Name, string(call.Args)); e2 != nil {
					r.loud("re-landed call", e2)
					continue
				}
				r.resultMap[call.ID] = append(r.resultMap[call.ID], storage)
			}
			r.mu.Unlock()
		case core.RoleTool:
			storage, ok := r.popStorage(m.ToolID)
			if !ok {
				continue
			}
			failure := r.originalErr(m.ToolID, seen[m.ToolID], counts[m.ToolID])
			seen[m.ToolID]++
			if e2 := RecordToolResult(context.Background(), r.db, r.sid, r.lastSeq, storage, m.Content, failure); e2 != nil {
				r.loud("re-landed result", e2)
			}
		}
	}
}

func (r *Recorder) originalErr(id string, i, total int) *string {
	if id == "" || total <= 0 {
		return nil
	}
	rows, err := r.db.DB.QueryContext(context.Background(),
		`SELECT "err" FROM "tool_calls" WHERE "session_id" = $1 AND "id" = $2 ORDER BY "message_seq" DESC`, r.sid, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var errs []string
	for rows.Next() {
		var e sql.NullString
		if err := rows.Scan(&e); err != nil {
			return nil
		}
		if e.Valid {
			errs = append(errs, e.String)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	idx := len(errs) - total + i
	if idx < 0 {
		idx = 0
	}
	if idx >= len(errs) {
		idx = len(errs) - 1
	}
	f := errs[idx]
	return &f
}

func (r *Recorder) upsertFiles() {
	if r.session == nil || r.snapshot == nil {
		return
	}
	files := r.snapshot(r.session)
	if len(files) == 0 {
		return
	}
	err := withTx(r.db, context.Background(), func(c context.Context) error {
		for path, st := range files {
			existing, err := safely(func() (*domain.File, error) {
				return domain.NewFileDomain().GetFile(c, r.sid, path).Row()
			})
			if err != nil {
				return err
			}
			row := domain.File{SessionId: r.sid, Path: path, Hash: st.Hash, Mtime: st.Mtime}
			if existing != nil {
				if _, err := domain.NewFileDomain().UpdateFile(c, row); err != nil {
					return err
				}
				continue
			}
			if _, err := domain.NewFileDomain().InsertFile(c, row); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		r.loud("files", err)
	}
}

func (r *Recorder) Close(exit string) error {
	if e := r.ensure(); e != nil {
		return e
	}
	return CloseSession(context.Background(), r.db, r.sid, exit)
}

func (r *Recorder) Ensure() error {
	return r.ensure()
}

func (r *Recorder) Retarget(sid string, session *core.Session) {
	r.mu.Lock()
	r.sid = sid
	r.session = session
	r.mu.Unlock()
}

func (r *Recorder) ensure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ensured {
		return nil
	}

	if func() bool {
		found := false
		_ = withTx(r.db, context.Background(), func(c context.Context) error {
			s, e := safely(func() (*domain.Session, error) {
				return domain.NewSessionDomain().GetSession(c, r.sid).Row()
			})
			if e == nil && s != nil {
				r.ensured = true
				found = true
			}
			return nil
		})
		return found
	}() {
		return nil
	}
	e := RecordSession(context.Background(), r.db, r.sid, r.cwd, r.model, r.version)
	if e == nil {
		r.ensured = true
	}
	return e
}

func (r *Recorder) setLastSeq(seq int64) {
	r.mu.Lock()
	r.lastSeq = seq
	r.mu.Unlock()
}

func (r *Recorder) loud(what string, err error) {
	fmt.Fprintf(os.Stderr, "rig state: session %s: %s: %v\n", r.sid, what, err)
}
