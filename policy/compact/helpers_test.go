package compact_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/models"
)

// scriptedTurn is one canned provider response, in call order.
type scriptedTurn struct {
	events  []core.Event
	err     error // pre-stream transport error (after the signal/block)
	signal  func()
	blockOn chan struct{} // block until closed (a steer dies in the window)
}

// scriptedProvider is a fake core.Provider with scripted streams and
// request capture. The DI seam: the policy's compact action and the
// decorator's retry both cross it.
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

// calls is the number of provider calls so far (main and summary both).
func (p *scriptedProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

// reqs is the captured requests, in call order (read after the streams
// the tests drive are drained: the channel close orders them).
func (p *scriptedProvider) reqs() []core.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]core.Request(nil), p.captured...)
}

// captureFrontend records the event order; Input EOFs (the policy's
// trigger path does not pull input).
type captureFrontend struct {
	mu     sync.Mutex
	events []core.Event
}

func (f *captureFrontend) Input(context.Context) (string, error) { return "", io.EOF }

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

// overflowRow is the overflow-recovery fixture row: a wider window than
// testRow so the older prefix (decision 3's summary input) fits the
// summary window when a fault-driven compact runs on a transcript far
// over the trigger. KeepRecent 200 keeps the split's tail at the last
// message, the atomic pair kept whole in TestKeepRecentCutsAtPairBoundary.
var overflowRow = models.Model{Role: models.RoleInteractive, ID: "local", Window: 4000, MaxTokens: 500, Reserve: 100, KeepRecent: 200}

// compactFixture is a transcript over the trigger of the test row, with
// an older prefix that is worth summarizing.
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

// stringify names an event for failure messages.
func stringify(ev core.Event) string { return fmt.Sprintf("%T %+v", ev, ev) }
