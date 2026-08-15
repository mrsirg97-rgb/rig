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

// Recorder is the observing Frontend and ToolExec tap: it forwards every
// Input/Notify call untouched to the inner frontend, and appends state
// rows for what the loop already emits — user messages, assistant text
// assembled per message, tool calls with their results, usage, faults, and
// session closure. Each row lands inside its own short transaction, so a
// kill leaves every completed row readable. Observation failures surface
// loudly and never disturb the turn.
type Recorder struct {
	inner   core.Frontend
	db      store.DB
	cwd     string
	model   string
	version string
	sid     string
	buffer  strings.Builder
	pending []core.ToolCall
	lastSeq int64
	ensured bool
	mu      sync.Mutex
}

func NewRecorder(inner core.Frontend, db store.DB, cwd, model, version, sid string) *Recorder {
	return &Recorder{
		inner: inner, db: db, cwd: cwd, model: model,
		version: version, sid: sid,
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
		r.buffer.WriteString(e.Text)
	case core.ToolCallEvent:
		r.mu.Lock()
		r.pending = append(r.pending, e.Call)
		r.mu.Unlock()
	case core.Done:
		seq := r.land()
		if seq == 0 {
			seq = r.lastSeq
		}
		if seq > 0 {
			// cache columns ride at zero until the transport reports them;
			// the schema is designed for that day already
			if e2 := RecordUsage(context.Background(), r.db, seq, int64(e.Usage.Prompt), int64(e.Usage.Completion), 0, 0); e2 != nil {
				r.loud("usage", e2)
			}
		}
	case core.Fault:
		if _, e2 := RecordFault(context.Background(), r.db, r.sid, now(), e.Err.Error()); e2 != nil {
			r.loud("fault", e2)
		}
		// the partial never lands: the session is preserved up to the last
		// complete message
		r.mu.Lock()
		r.buffer.Reset()
		r.pending = nil
		r.mu.Unlock()
	}
}

// land flushes the assembled text and the pending calls into one assistant
// row — written even when its content is empty — and lands the calls
// against it. The tool-ID marker names a single call; a multi-call turn
// leaves it unset, the calls carrying their own attribution.
func (r *Recorder) land() (seq int64) {
	r.mu.Lock()
	text := r.buffer.String()
	calls := r.pending
	r.buffer.Reset()
	r.pending = nil
	r.mu.Unlock()
	if text == "" && len(calls) == 0 {
		return 0
	}
	var toolID *string
	if len(calls) == 1 {
		id := calls[0].ID
		toolID = &id
	}
	var e error
	seq, e = RecordMessage(context.Background(), r.db, r.sid, "assistant", text, nil, toolID)
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

// Observe taps the tool-execution seam: the guarded result, named.
func (r *Recorder) Observe(next core.ToolExec) core.ToolExec {
	return func(ctx context.Context, call core.ToolCall) (string, error) {
		if e := r.ensure(); e != nil {
			r.loud("session row", e)
		}
		result, err := next(ctx, call)
		var failure *string
		if err != nil {
			f := err.Error()
			failure = &f
		}
		if e := RecordToolResult(ctx, r.db, call.ID, result, failure); e != nil {
			r.loud("tool result "+call.ID, e)
		}
		return result, err
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
