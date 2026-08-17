package state

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state/domain"
)

// Recorder is the observing Frontend: it forwards every Input/Notify call
// untouched to the inner frontend, and appends state rows for what the
// loop already emits — user messages, assistant text and reasoning
// assembled per message, tool calls with their results (the loop's
// ToolResult event, the Observe tap retired), usage with the cache
// columns, the files snapshot at the turn boundary, faults, and session
// closure. Each row lands inside its own short transaction, so a kill
// leaves every completed row readable. Observation failures surface
// loudly and never disturb the turn. The rule (SPEC_HARDENING decision
// 4): an unlanded partial at any TurnEnd is a partial and is discarded.
type Recorder struct {
	inner   core.Frontend
	db      store.DB
	cwd     string
	model   string
	version string
	sid     string
	session *core.Session // root-owned; the files snapshot reads it at the boundary
	buffer  strings.Builder
	reason  strings.Builder
	pending []core.ToolCall
	lastSeq int64
	relandN int // the re-landing id suffix counter (SPEC_COMPACT 5)
	ensured bool
	mu      sync.Mutex
}

func NewRecorder(inner core.Frontend, db store.DB, cwd, model, version, sid string, session *core.Session) *Recorder {
	return &Recorder{
		inner: inner, db: db, cwd: cwd, model: model,
		version: version, sid: sid, session: session,
	}
}

func (r *Recorder) Input(ctx context.Context) (string, error) {
	if e := r.ensure(); e != nil {
		r.loud("session row", e)
	}
	// transcript order: anything pending from before lands before the user row
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
	r.upsertFiles() // turn boundary: the session's files snapshot, as it stands
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
		// the loop's event carries the guarded result, named: the Observe
		// tap in the chain is retired (SPEC_HARDENING decision 1).
		if e := r.ensure(); e != nil {
			r.loud("session row", e)
		}
		var failure *string
		if e.Err != nil {
			f := e.Err.Error()
			failure = &f
		}
		if e2 := RecordToolResult(context.Background(), r.db, e.ID, e.Content, failure); e2 != nil {
			r.loud("tool result "+e.ID, e2)
		}
	case core.Done:
		seq := r.land()
		if seq == 0 {
			seq = r.lastSeq
		}
		if seq > 0 {
			if e2 := RecordUsage(context.Background(), r.db, seq, int64(e.Usage.Prompt), int64(e.Usage.Completion), int64(e.Usage.CacheRead), int64(e.Usage.CacheWrite)); e2 != nil {
				r.loud("usage", e2)
			}
		}
		r.upsertFiles() // turn boundary: the session's files snapshot
	case core.Compacted:
		// SPEC_COMPACT 5: the summary lands as a marked user row plus a
		// usage row, and the kept tail is re-landed after it (fresh seqs,
		// fresh call ids) so the resume projection — which starts from the
		// last [compaction] row — rebuilds the compacted shape, not the
		// full history.
		r.landCompacted(e)
	case core.Fault:
		if _, e2 := RecordFault(context.Background(), r.db, r.sid, now(), e.Err.Error()); e2 != nil {
			r.loud("fault", e2)
		}
		r.discardPartial()
	case core.TurnEnd:
		// the rule: an unlanded partial at any TurnEnd is a partial and is
		// discarded — subsuming the Fault-time discard, and covering the
		// interrupt, which has no Fault (the "PARTIAL fresh" bug reversed).
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

// land flushes the assembled text, reasoning, and the pending calls into
// one assistant row — written even when its content is empty — and lands
// the calls against it. The tool-ID marker names a single call; a
// multi-call turn leaves it unset, the calls carrying their own
// attribution.
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
	for _, call := range calls {
		if e2 := RecordToolCall(context.Background(), r.db, seq, call.ID, call.Name, string(call.Args)); e2 != nil {
			r.loud("tool call "+call.ID, e2)
		}
	}
	return seq
}

// landCompacted lands a compaction (SPEC_COMPACT 5): the summary as a
// marked user row plus a usage row, then re-lands the kept tail after it.
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

// relandTail re-lands the kept tail after the summary row (SPEC_COMPACT
// 5): at the Compacted moment the root's session is exactly [summary row]
// + tail, so the tail is the session's messages after the first. Fresh
// rows (fresh seqs); the assistant calls carry recorder-minted fresh ids
// (the tool_calls.id primary key), name/args/result verbatim, so the
// call/result pair stays consistent within the copy; the earlier rows
// stay in the store as the autopsy. Duplicates bounded by the tail
// (KeepRecent + one batch). A dangling result whose call is not in the
// tail has nothing fresh to attach to — the original row is the autopsy.
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
	r.mu.Lock()
	r.relandN++
	suffix := fmt.Sprintf("-r%d", r.relandN)
	r.mu.Unlock()
	idMap := map[string]string{}
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
			for _, call := range m.ToolCalls {
				fresh := call.ID + suffix
				idMap[call.ID] = fresh
				if e2 := RecordToolCall(context.Background(), r.db, seq, fresh, call.Name, string(call.Args)); e2 != nil {
					r.loud("re-landed call", e2)
				}
			}
		case core.RoleTool:
			fresh, ok := idMap[m.ToolID]
			if !ok {
				continue // a dangling result: nothing fresh to attach to
			}
			if e2 := RecordToolResult(context.Background(), r.db, fresh, m.Content, nil); e2 != nil {
				r.loud("re-landed result", e2)
			}
		}
	}
}

// upsertFiles snapshots the session's file provenance at the turn
// boundary: a drifted row is replaced, a new path inserted (the files
// table is keyed by session + path). This is what lets a resumed session
// keep its drift checks.
func (r *Recorder) upsertFiles() {
	if r.session == nil {
		return
	}
	r.mu.Lock()
	files := make(map[string]core.FileState, len(r.session.Files))
	for p, st := range r.session.Files {
		files[p] = st
	}
	r.mu.Unlock()
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

// Ensure is the exported lazy session-row creation (SPEC_COMMANDS 4's
// handoff, step 2): the row exists before any row lands under the id.
// Idempotent: a pre-existing row is adopted, never re-inserted.
func (r *Recorder) Ensure() error {
	return r.ensure()
}

// Retarget re-points the retiring recorder (SPEC_COMMANDS 4's handoff,
// step 3): the swap re-points the retiring recorder before it completes
// — its in-flight Input lands the user row (and the files snapshot)
// under the new session's id, then retires. The new recorder is already
// built over the new session; this is the in-flight row's adoption.
func (r *Recorder) Retarget(sid string, session *core.Session) {
	r.mu.Lock()
	r.sid = sid
	r.session = session
	r.mu.Unlock()
}

// ensure lands the session row before any observation appends to it —
// lazily, once, inside its own short transaction.
func (r *Recorder) ensure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ensured {
		return nil
	}
	// idempotent: a pre-existing session row is adopted, never re-inserted
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
