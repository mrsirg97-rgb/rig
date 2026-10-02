package decision

import (
	"context"
	"fmt"
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
	ch   chan Pending
	dec  Decider
	sink Sink
	wake func()
	loud func(string)
}

func NewQueue(dec Decider, sink Sink, wake func(), loud func(string)) *Queue {
	return &Queue{
		ch:   make(chan Pending, QueueCap),
		dec:  dec,
		sink: sink,
		wake: wake,
		loud: loud,
	}
}

func (q *Queue) Propose(p Pending) {
	select {
	case q.ch <- p:
	default:
		q.say("decision: queue full, dropping the %s proposal", p.Site)
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
		q.say("decision: decide %s: %v", p.Site, err)
		return
	}
	for _, a := range answers {
		if a.Question != p.Question.ID {
			continue
		}
		if a.Confidence < 0 || a.Confidence > 1 {
			q.say("decision: %s answered %q with confidence %g, not a probability", p.Site, a.Value, a.Confidence)
			continue
		}
		if err := q.sink.ProposePending(ctx, p, a); err != nil {
			q.say("decision: propose %s: %v", p.Site, err)
			continue
		}
		if q.wake != nil {
			q.wake()
		}
	}
}

func (q *Queue) say(format string, args ...any) {
	if q.loud != nil {
		q.loud(fmt.Sprintf(format, args...))
	}
}
