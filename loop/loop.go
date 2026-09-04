package loop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mrsirg97-rgb/rig"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/evt"
)

const (
	prioInput  = 90
	prioStream = 50
	prioTool   = 50
)

type turn struct {
	ctx       context.Context
	cancel    context.CancelFunc
	text      strings.Builder
	reasoning strings.Builder
	calls     []core.ToolCall
	done      bool
	faulted   bool
	usage     core.Usage
	reason    core.TurnReason
	batch     *batch
	results   []*outcome
	cursor    int
	started   int
}

type run struct {
	ctx    context.Context
	k      *rig.Kernel
	engine evt.Engine
	exec   core.ToolExec
	specs  []core.ToolSpec
	turn   *turn
	err    error
}

func Run(ctx context.Context, k *rig.Kernel) error {
	if k.Provider == nil {
		return errors.New("loop: kernel has no provider registered")
	}
	if k.Frontend == nil {
		return errors.New("loop: kernel has no frontend registered")
	}
	if k.Policy == nil {
		return errors.New("loop: kernel has no context policy registered")
	}
	if k.Session == nil {
		k.Session = core.NewSession()
	}

	tools := make(map[string]core.Tool, len(k.Tools))
	specs := make([]core.ToolSpec, 0, len(k.Tools))
	for _, t := range k.Tools {
		tools[t.Name()] = t
		specs = append(specs, core.ToolSpec{Name: t.Name(), Description: t.Description(), Schema: t.Schema()})
	}

	var exec core.ToolExec = directExec(tools)
	for _, mw := range k.Middleware {
		exec = mw.Wrap(exec)
	}

	r := &run{ctx: ctx, k: k, engine: evt.NewEngine(), exec: exec, specs: specs}
	r.post(prioInput, r.prompt)
	r.engine.Start(context.Background())
	return r.err
}

func (r *run) post(priority int, f func()) {
	r.engine.Add(evt.Func(func(context.Context) { f() }), priority)
}

func (r *run) stop(err error) {
	r.err = err
	r.engine.Stop()
}

func (r *run) prompt() {
	if r.ctx.Err() != nil {
		r.stop(nil)
		return
	}
	turnCtx, turnCancel := context.WithCancel(r.ctx)
	t := &turn{ctx: turnCtx, cancel: turnCancel, reason: core.TurnOver}
	r.turn = t
	go func() {
		msg, err := r.k.Frontend.Input(core.WithInterrupt(turnCtx, turnCancel))
		r.post(prioInput, func() { r.input(t, msg, err) })
	}()
}

func (r *run) input(t *turn, msg string, err error) {
	if t != r.turn {
		return
	}
	if err != nil {
		t.cancel()
		if r.ctx.Err() != nil || errors.Is(err, io.EOF) {
			r.stop(nil)
			return
		}
		if t.ctx.Err() != nil {
			r.prompt()
			return
		}
		r.k.Frontend.Notify(core.Fault{Err: err})
		r.stop(err)
		return
	}
	if strings.TrimSpace(msg) == "" {
		t.cancel()
		r.prompt()
		return
	}

	session := r.k.Session
	session.Append(core.Message{Role: core.RoleUser, Content: msg})

	for _, mw := range r.k.Middleware {
		if obs, ok := mw.(core.TurnObserver); ok {
			obs.TurnStart(t.ctx, session)
		}
	}
	r.model(t)
}

func (r *run) model(t *turn) {
	session := r.k.Session
	msgs, err := r.k.Policy.Assemble(t.ctx, session)
	if err != nil {
		if t.ctx.Err() != nil {
			t.reason = core.TurnInterrupt
		} else {
			r.k.Frontend.Notify(core.Fault{Err: err})
			t.reason = core.TurnFault
		}
		r.end(t)
		return
	}

	events, err := r.k.Provider.Stream(t.ctx, core.Request{Messages: msgs, Tools: r.specs})
	if err != nil {
		if t.ctx.Err() != nil {
			t.reason = core.TurnInterrupt
		} else {
			r.k.Frontend.Notify(core.Fault{Err: err})
			t.reason = core.TurnFault
		}
		r.end(t)
		return
	}

	t.text.Reset()
	t.reasoning.Reset()
	t.calls = nil
	t.done, t.faulted = false, false
	t.usage = core.Usage{}
	go func() {
		for ev := range events {
			ev := ev
			r.post(prioStream, func() { r.streamEvent(t, ev) })
		}
		r.post(prioStream, func() { r.streamEnd(t) })
	}()
}

func (r *run) streamEvent(t *turn, ev core.Event) {
	if t != r.turn {
		return
	}
	switch e := ev.(type) {
	case core.TextDelta:
		r.k.Frontend.Notify(ev)
		t.text.WriteString(e.Text)
	case core.ReasoningDelta:
		r.k.Frontend.Notify(ev)
		t.reasoning.WriteString(e.Text)
	case core.ToolCallEvent:
		r.k.Frontend.Notify(ev)
		t.calls = append(t.calls, e.Call)
	case core.Done:
		r.k.Frontend.Notify(ev)
		t.done = true
		t.usage = e.Usage
	case core.Fault:
		r.k.Frontend.Notify(ev)
		t.faulted = true
		if t.ctx.Err() != nil {
			t.reason = core.TurnInterrupt
		} else {
			t.reason = core.TurnFault
		}
	default:
		r.k.Frontend.Notify(ev)
	}
}

func (r *run) streamEnd(t *turn) {
	if t != r.turn {
		return
	}
	session := r.k.Session
	switch {
	case t.faulted:
		r.end(t)
	case !t.done:
		if r.ctx.Err() != nil {
			t.cancel()
			r.stop(nil)
			return
		}
		if t.ctx.Err() != nil {
			t.reason = core.TurnInterrupt
			r.end(t)
			return
		}
		err := errors.New("loop: provider closed the stream without Done or Fault")
		r.k.Frontend.Notify(core.Fault{Err: err})
		t.cancel()
		r.k.Frontend.Notify(core.TurnEnd{Reason: core.TurnFault})
		r.stop(err)
	case len(t.calls) == 0:
		if t.text.Len() > 0 || t.reasoning.Len() > 0 {
			session.Append(core.Message{Role: core.RoleAssistant, Content: t.text.String(), Reasoning: t.reasoning.String(), ContextTokens: t.usage.Prompt + t.usage.Completion})
		}
		r.end(t)
	default:
		session.Append(core.Message{
			Role:          core.RoleAssistant,
			Content:       t.text.String(),
			Reasoning:     t.reasoning.String(),
			ToolCalls:     t.calls,
			ContextTokens: t.usage.Prompt + t.usage.Completion,
		})
		t.batch = newBatch(core.WithSession(t.ctx, session), r.exec, t.calls, r.k.Concurrent, r.k.Parallel, func(x int, out outcome) {
			r.post(prioTool, func() { r.toolDone(t, x, out) })
		})
		t.results = make([]*outcome, len(t.calls))
		t.cursor, t.started = 0, 0
		r.advance(t)
	}
}

func (r *run) toolDone(t *turn, x int, out outcome) {
	if t != r.turn {
		return
	}
	t.results[x] = &out
	r.advance(t)
}

func (r *run) advance(t *turn) {
	session := r.k.Session
	for t.cursor < len(t.calls) {
		i := t.cursor
		call := t.calls[i]
		if t.started == i {
			r.k.Frontend.Notify(core.ToolStart{Call: call})
			t.started++
			t.batch.dispatch(i)
		}
		out := t.results[i]
		if out == nil {
			return
		}
		content, execErr := out.content, out.err
		if execErr != nil && content == "" {
			content = execErr.Error()
		}
		r.k.Frontend.Notify(core.ToolResult{ID: call.ID, Content: content, Err: execErr, Duration: out.dur})
		session.Append(core.Message{
			Role:    core.RoleTool,
			ToolID:  call.ID,
			Content: content,
		})
		t.cursor++
	}
	r.model(t)
}

func (r *run) end(t *turn) {
	t.cancel()
	r.k.Frontend.Notify(core.TurnEnd{Reason: t.reason})
	r.turn = nil
	r.prompt()
}

func directExec(tools map[string]core.Tool) core.ToolExec {
	return func(ctx context.Context, call core.ToolCall) (string, error) {
		t, ok := tools[call.Name]
		if !ok {
			msg := fmt.Sprintf("unknown tool: %s", call.Name)
			return msg, errors.New(msg)
		}
		return t.Exec(ctx, call.Args)
	}
}
