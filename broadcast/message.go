package broadcast

import (
	"github.com/mrsirg97-rgb/rig/v2/core"
)

/*
Message

	Message is the communication contract for Member.
	it provides both the origin (the member), member health, and the event it carries
	it can be utilized for both heartbeat protocol and message passing
	a message carrying no event is a heartbeat
*/
type Message interface {
	Origin() int64
	Ok() bool
	Event() core.Event
}

type message struct {
	origin int64
	ok     bool
	event  core.Event
}

func NewMessage(origin int64, ok bool, event ...core.Event) Message {
	message := &message{origin: origin, ok: ok}
	if len(event) > 0 {
		message.event = event[0]
	}
	return message
}

func Heartbeat(origin int64, ok bool) Message {
	return NewMessage(origin, ok)
}

func (m *message) Origin() int64 {
	return m.origin
}

func (m *message) Ok() bool {
	return m.ok
}

func (m *message) Event() core.Event {
	return m.event
}
