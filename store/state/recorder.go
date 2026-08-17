package state

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/mrsirg97-rgb/looper/core"
	"github.com/mrsirg97-rgb/looper/store"
	"github.com/mrsirg97-rgb/looper/store/state/domain"
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

// upsertFiles snapshots the session's file provenance at the turn
// boundary: a drifted row is replaced, a new path inserted (the files
// table is keyed by session + path). Without this, RecordFile has no
// production caller and SPEC_STATE's "a resumed session keeps its drift
// checks" is owed by the schema and unmet by the writer (SPEC_HARDENING
// decision 5 names the gap and closes it).
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
	fmt.Fprintf(os.Stderr, "looper state: session %s: %s: %v\n", r.sid, what, err)
}
