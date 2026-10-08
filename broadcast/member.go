package broadcast

import (
	"context"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

type Member interface {
	Id() int64
	Room() string
	Leave() bool
	Forward(ctx context.Context, callback func(target int64, ack error), messages ...Message)
	Publish(ctx context.Context, callback func(ack error), messages ...Message)
	Subscribe(ctx context.Context, callback func(err error, messages ...Message))
}

type member struct {
	room      Room
	transport Transport
}

func NewMember(room Room, transport Transport) Member {
	return &member{room: room, transport: transport}
}

func (m *member) Id() int64 {
	return m.transport.Id()
}

func (m *member) Room() string {
	return m.room.Id()
}

func (m *member) Leave() bool {
	m.transport.Close()
	return m.room.Remove(m.Id())
}

func (m *member) Forward(ctx context.Context, callback func(target int64, ack error), messages ...Message) {
	m.transport.Send(ctx, func(ack error) {
		callback(m.Id(), ack)
	}, messages...)
}

func (m *member) Publish(ctx context.Context, callback func(ack error), messages ...Message) {
	callback(m.room.Broadcast(ctx, m.Id(), messages...))
}

func (m *member) Subscribe(ctx context.Context, callback func(err error, messages ...Message)) {
	m.transport.Recv(ctx, callback)
}

func Say(m Member, source, text string, level ...core.Level) {
	n := core.Notice{Source: source, Text: text}
	if len(level) > 0 {
		n.Level = level[0]
	}
	m.Publish(context.Background(), func(error) {}, NewMessage(m.Id(), true, n))
}
