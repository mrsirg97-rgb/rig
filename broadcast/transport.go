package broadcast

import (
	"context"
	"fmt"
	"sync"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/evt"
)

/*
Transport

	Transport provides the abstraction for Members to communicate with each other in a room.
	it defines the minimum contract necessary for send/receive and acknowledgements
*/
type Transport interface {
	Id() int64
	Send(ctx context.Context, callback func(ack error), messages ...Message)
	Recv(ctx context.Context, callback func(err error, messages ...Message))
	Close()
}

/*
loopTransport

	loopTransport delivers through the event loop: a send is a closure posted at the transport's priority,
	the ack is the post (the queue is the durability, an event stays until the consumer runs it),
	and the receive is the callback the closure resolves to on the loop's goroutine.
	a heartbeat, or a snapshot (core.Snapshot), is a state and not a story: one waits per sender,
	and a later one before it ran replaces its value instead of posting again.
*/
type loopTransport struct {
	id       int64
	engine   evt.Engine
	priority int

	mu      sync.Mutex
	recv    func(err error, messages ...Message)
	closed  bool
	pending map[string]*latest
}

type latest struct {
	message Message
}

func NewLoopTransport(id int64, engine evt.Engine, priority int) Transport {
	return &loopTransport{id: id, engine: engine, priority: priority, pending: map[string]*latest{}}
}

func (t *loopTransport) Id() int64 {
	return t.id
}

func stateKey(messages []Message) (string, bool) {
	if len(messages) != 1 {
		return "", false
	}
	m := messages[0]
	switch ev := m.Event().(type) {
	case nil:
		return fmt.Sprintf("%d/heartbeat", m.Origin()), true
	case core.Snapshot:
		return fmt.Sprintf("%d/%T", m.Origin(), ev), true
	}
	return "", false
}

func (t *loopTransport) Send(ctx context.Context, callback func(ack error), messages ...Message) {
	if err := ctx.Err(); err != nil {
		callback(err)
		return
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		callback(context.Canceled)
		return
	}
	key, state := stateKey(messages)
	if state {
		if slot, waiting := t.pending[key]; waiting {
			slot.message = messages[0]
			t.mu.Unlock()
			callback(nil)
			return
		}
		t.pending[key] = &latest{message: messages[0]}
	}
	t.mu.Unlock()
	t.engine.Add(evt.Func(func(context.Context) {
		t.mu.Lock()
		recv, closed := t.recv, t.closed
		if state {
			messages = []Message{t.pending[key].message}
			delete(t.pending, key)
		}
		t.mu.Unlock()
		if closed || recv == nil {
			return
		}
		recv(nil, messages...)
	}), t.priority)
	callback(nil)
}

func (t *loopTransport) Recv(ctx context.Context, callback func(err error, messages ...Message)) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		callback(context.Canceled)
		return
	}
	t.recv = callback
	t.mu.Unlock()
	context.AfterFunc(ctx, func() {
		t.mu.Lock()
		if t.recv != nil {
			t.recv = nil
		}
		t.mu.Unlock()
		callback(ctx.Err())
	})
}

func (t *loopTransport) Close() {
	t.mu.Lock()
	t.closed = true
	t.recv = nil
	t.mu.Unlock()
}
