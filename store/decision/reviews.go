package decision

import (
	"context"

	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/store"
)

type Reviews struct{ DB store.DB }

func (r Reviews) Pending(ctx context.Context) ([]decision.ReviewRow, error) {
	rows, err := Pending(ctx, r.DB)
	if err != nil {
		return nil, err
	}
	out := make([]decision.ReviewRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, decision.ReviewRow{
			ID: row.ID, Site: row.Site, State: row.State, Question: row.Question,
			Answer: row.Answer, Confidence: row.Confidence, Decider: row.Decider,
		})
	}
	return out, nil
}

func (r Reviews) Settle(ctx context.Context, id int64, approved bool, reviewer, answer string) error {
	return Settle(ctx, r.DB, SettleInput{
		ID: id, Approved: approved, Reviewer: reviewer, ReviewerAnswer: answer,
	})
}
