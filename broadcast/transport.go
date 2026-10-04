package broadcast

import (
	"context"
	"sync"

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
	a heartbeat already waiting in the queue is not posted again: one pending heartbeat per member.
*/
type loopTransport struct {
	id       int64
	engine   evt.Engine
	priority int

	mu        sync.Mutex
	recv      func(err error, messages ...Message)
	closed    bool
	heartbeat bool
}

func NewLoopTransport(id int64, engine evt.Engine, priority int) Transport {
	return &loopTransport{id: id, engine: engine, priority: priority}
}

func (t *loopTransport) Id() int64 {
	return t.id
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
	if len(messages) == 1 && messages[0].Event() == nil {
		if t.heartbeat {
			t.mu.Unlock()
			callback(nil)
			return
		}
		t.heartbeat = true
	}
	t.mu.Unlock()
	t.engine.Add(evt.Func(func(context.Context) {
		t.mu.Lock()
		recv, closed := t.recv, t.closed
		if len(messages) == 1 && messages[0].Event() == nil {
			t.heartbeat = false
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
