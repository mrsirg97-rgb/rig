package decision

import (
	"context"
	"fmt"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
)

const QueueCap = 256

type Pending struct {
	Site     string
	Scope    string
	Session  string
	State    string
	Question Question
}

type Proposer interface {
	Propose(p Pending)
}

type Sink interface {
	ProposePending(ctx context.Context, p Pending, a Answer) error
}

type Queue struct {
	ch    chan Pending
	dec   Decider
	sink  Sink
	land  func()
	voice broadcast.Member
}

func NewQueue(dec Decider, sink Sink, land func(), voice broadcast.Member) *Queue {
	return &Queue{
		ch:    make(chan Pending, QueueCap),
		dec:   dec,
		sink:  sink,
		land:  land,
		voice: voice,
	}
}

func (q *Queue) Propose(p Pending) {
	select {
	case q.ch <- p:
	default:
		q.say("queue full, dropping the %s proposal", p.Site)
	}
}

func (q *Queue) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-q.ch:
			q.decide(ctx, p)
		}
	}
}

func (q *Queue) decide(ctx context.Context, p Pending) {
	answers, err := q.dec.Decide(ctx, p.State, []Question{p.Question})
	if err != nil {
		q.say("decide %s: %v", p.Site, err)
		return
	}
	for _, a := range answers {
		if a.Question != p.Question.ID {
			continue
		}
		if a.Confidence < 0 || a.Confidence > 1 {
			q.say("%s answered %q with confidence %g, not a probability", p.Site, a.Value, a.Confidence)
			continue
		}
		if err := q.sink.ProposePending(ctx, p, a); err != nil {
			q.say("propose %s: %v", p.Site, err)
			continue
		}
		if q.land != nil {
			q.land()
		}
	}
}

func (q *Queue) say(format string, args ...any) {
	if q.voice != nil {
		broadcast.Say(q.voice, "decision", fmt.Sprintf(format, args...))
	}
}
