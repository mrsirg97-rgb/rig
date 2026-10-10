package compact_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/models"
)

type scriptedTurn struct {
	events  []core.Event
	err     error
	signal  func()
	blockOn chan struct{}
}

type scriptedProvider struct {
	mu       sync.Mutex
	turns    []scriptedTurn
	n        int
	captured []core.Request
}

func (p *scriptedProvider) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	p.mu.Lock()
	if p.n >= len(p.turns) {
		p.mu.Unlock()
		panic("scriptedProvider: more model calls than scripted")
	}
	turn := p.turns[p.n]
	p.n++
	p.captured = append(p.captured, req)
	p.mu.Unlock()
	if turn.signal != nil {
		turn.signal()
	}
	if turn.blockOn != nil {
		select {
		case <-turn.blockOn:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if turn.err != nil {
		return nil, turn.err
	}
	out := make(chan core.Event, len(turn.events)+1)
	go func() {
		defer close(out)
		for _, ev := range turn.events {
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (p *scriptedProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

func (p *scriptedProvider) reqs() []core.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]core.Request(nil), p.captured...)
}

type captureFrontend struct {
	mu     sync.Mutex
	inputs []string
	cancel context.CancelFunc
	events []core.Event
}

func (f *captureFrontend) Input(ctx context.Context) (string, error) {
	if cancel, ok := core.InterruptFrom(ctx); ok {
		f.mu.Lock()
		f.cancel = cancel
		f.mu.Unlock()
	}
	f.mu.Lock()
	if len(f.inputs) == 0 {
		f.mu.Unlock()
		return "", io.EOF
	}
	s := f.inputs[0]
	f.inputs = f.inputs[1:]
	f.mu.Unlock()
	return s, nil
}

func (f *captureFrontend) Notify(ev core.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *captureFrontend) snapshot() []core.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core.Event(nil), f.events...)
}

func (f *captureFrontend) steal() context.CancelFunc {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.cancel
	f.cancel = nil
	return c
}

var overflowRow = models.Model{Role: models.RoleInteractive, ID: "local", Window: 4000, MaxTokens: 500, Reserve: 100, KeepRecent: 200}

func compactFixture() *core.Session {
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 2000)})
	s.Append(core.Message{
		Role: core.RoleAssistant, Content: strings.Repeat("a", 400),
		ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: []byte(`{"command":"x"}`)}},
	})
	s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 2000)})
	return s
}

func stringify(ev core.Event) string { return fmt.Sprintf("%T %+v", ev, ev) }

func stripCue(evs []core.Event) []core.Event {
	out := evs[:0:0]
	for _, ev := range evs {
		switch ev.(type) {
		case core.Compacting, core.Phase:
			continue
		}
		out = append(out, ev)
	}
	return out
}
