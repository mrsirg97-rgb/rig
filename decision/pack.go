package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
)

const packQuestionID = "matter"

var packQuestion = Question{
	ID:     packQuestionID,
	Kind:   KindYesNo,
	Prompt: "Does this symbol matter for the task?",
}

type packState struct {
	Task string `json:"task"`
	Item string `json:"item"`
}

type PackVerdict struct {
	Answered    bool
	Yes         bool
	Unsure      bool
	Probability float64
}

type PackScorer struct {
	dec      Decider
	sink     Sink
	land     func()
	voice    broadcast.Member
	parallel int
}

func NewPackScorer(dec Decider, sink Sink, land func(), voice broadcast.Member, parallel int) (*PackScorer, error) {
	if dec == nil {
		return nil, errors.New("decision: no decider")
	}
	if parallel <= 0 {
		return nil, fmt.Errorf("decision: parallel %d: the fan-out needs a bound", parallel)
	}
	return &PackScorer{dec: dec, sink: sink, land: land, voice: voice, parallel: parallel}, nil
}

func (s *PackScorer) Score(ctx context.Context, task string, items []string) ([]PackVerdict, error) {
	states := make([]string, len(items))
	for i, item := range items {
		b, err := json.Marshal(packState{Task: task, Item: item})
		if err != nil {
			return nil, fmt.Errorf("decision: pack: item %d: %w", i+1, err)
		}
		states[i] = string(b)
	}
	answers, errs := FanOut(ctx, s.dec, s.parallel, packQuestion, states)
	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("decision: pack: item %d: %w; nothing was scored and no row was written", i+1, err)
		}
	}
	out := make([]PackVerdict, len(items))
	for i, a := range answers {
		if a.Question != packQuestion.ID {
			continue
		}
		out[i] = PackVerdict{
			Answered:    true,
			Yes:         a.Value == "yes" && a.Confidence >= unsureUnder,
			Unsure:      a.Confidence < unsureUnder,
			Probability: a.Confidence,
		}
		s.record(ctx, states[i], a)
	}
	return out, nil
}

func (s *PackScorer) record(ctx context.Context, state string, a Answer) {
	if s.sink == nil {
		return
	}
	session := ""
	if sess, ok := core.SessionFrom(ctx); ok {
		session = sess.ID
	}
	if err := s.sink.ProposePending(ctx, Pending{
		Site:     SitePack,
		Session:  session,
		State:    state,
		Question: packQuestion,
	}, a); err != nil {
		s.say("pack: %v", err)
		return
	}
	if s.land != nil {
		s.land()
	}
}

func (s *PackScorer) say(format string, args ...any) {
	if s.voice != nil {
		broadcast.Say(s.voice, "decision", fmt.Sprintf(format, args...))
	}
}
