package broadcast

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

/*
Room

	Rooms are groups of clients, represented as Member. This becomes the message passing abstraction.
	adding a member will register it in the room and make it available to broadcast from other members.
	each member can publish messages to the entire group or forward messages to individual users.
	a broadcast fans out to every other member and collects one ack per member; the error names the members that did not take it.
	the room is built with the transport its members speak through, so the room never names one.
*/
type Room interface {
	Id() string
	Add(origin int64) Member
	Remove(origin int64) bool
	Members() []int64
	Broadcast(ctx context.Context, origin int64, messages ...Message) error
}

type room struct {
	id        string
	transport func(origin int64) Transport
	mu        sync.Mutex
	registry  map[int64]Member
}

func NewRoom(id string, transport func(origin int64) Transport) Room {
	return &room{id: id, transport: transport, registry: make(map[int64]Member)}
}

func (r *room) Id() string {
	return r.id
}

func (r *room) Add(origin int64) Member {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.registry[origin]; !ok {
		r.registry[origin] = NewMember(r, r.transport(origin))
	}
	return r.registry[origin]
}

func (r *room) Remove(origin int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.registry[origin]
	delete(r.registry, origin)
	return ok
}

func (r *room) Members() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]int64, 0, len(r.registry))
	for id := range r.registry {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (r *room) Broadcast(ctx context.Context, origin int64, messages ...Message) error {
	if len(messages) == 0 {
		return nil
	}
	r.mu.Lock()
	targets := make([]Member, 0, len(r.registry))
	for id, m := range r.registry {
		if id != origin {
			targets = append(targets, m)
		}
	}
	r.mu.Unlock()
	slices.SortFunc(targets, func(a, b Member) int { return int(a.Id() - b.Id()) })
	var missed []string
	for _, m := range targets {
		m.Forward(ctx, func(target int64, ack error) {
			if ack != nil {
				missed = append(missed, fmt.Sprintf("%d: %v", target, ack))
			}
		}, messages...)
	}
	if len(missed) == 0 {
		return nil
	}
	return errors.New("broadcast missed " + strings.Join(missed, ", "))
}
