package decision

import (
	"context"
	"fmt"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/store"
)

type Recorder struct {
	DB    store.DB
	Scope string
	Log   func(string)
}

func (r Recorder) Record(ctx context.Context, f decision.Final) {
	session := ""
	if s, ok := core.SessionFrom(ctx); ok {
		session = s.ID
	}
	scope := f.Scope
	if scope == "" {
		scope = r.Scope
	}
	_, err := RecordFinal(ctx, r.DB, FinalInput{
		Scope:      scope,
		Session:    session,
		Site:       f.Site,
		State:      f.State,
		Question:   f.Question,
		Answer:     f.Answer,
		Confidence: f.Confidence,
		Unsure:     f.Unsure,
		Decider:    f.Decider,
	})
	if err != nil && r.Log != nil {
		r.Log(fmt.Sprintf("decision: record %s: %v", f.Site, err))
	}
}
