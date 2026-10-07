package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisiondomain "github.com/mrsirg97-rgb/rig/v2/store/decision/domain"
)

func Gold(ctx context.Context, db store.DB) ([]decision.GoldRow, error) {
	_, tx, err := db.TxReadOnly(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT "id", "state", "question", "answer", "reviewer_answer", "status" FROM "decisions"
		 WHERE "status" IN ('approved', 'denied') ORDER BY "id"`)
	if err != nil {
		return nil, fmt.Errorf("decision: gold: %w", err)
	}
	defer rows.Close()
	var out []decision.GoldRow
	for rows.Next() {
		var r decision.GoldRow
		var qjson string
		if err := rows.Scan(&r.ID, &r.State, &qjson, &r.Answer, &r.Corrected, &r.Status); err != nil {
			return nil, fmt.Errorf("decision: gold scan: %w", err)
		}
		if err := json.Unmarshal([]byte(qjson), &r.Question); err != nil {
			continue
		}
		r.Question.Kind = kindOf(r.Question.Kind)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("decision: gold: %w", err)
	}
	return out, nil
}

type TrainingInput struct {
	Scope      string
	Trainer    string
	Rows       int
	Skipped    int
	TrainRows  int
	HeldRows   int
	RunDir     string
	Checkpoint string
	Constant   string
	Incumbent  string
	Candidate  string
	Promoted   bool
}

func RecordTraining(ctx context.Context, db store.DB, in TrainingInput) (int64, error) {
	if in.Trainer == "" {
		return 0, fmt.Errorf("decision: training: no trainer")
	}
	if in.Scope == "" {
		return 0, fmt.Errorf("decision: training: no scope")
	}
	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX("id"), 0) + 1 FROM "trainings"`).Scan(&id); err != nil {
		return 0, fmt.Errorf("decision: training: mint id: %w", err)
	}
	row, err := decisiondomain.NewTrainingDomain().InsertTraining(bound, decisiondomain.Training{
		Id:         id,
		Scope:      in.Scope,
		Trainer:    in.Trainer,
		Rows:       int64(in.Rows),
		Skipped:    int64(in.Skipped),
		TrainRows:  int64(in.TrainRows),
		HeldRows:   int64(in.HeldRows),
		RunDir:     in.RunDir,
		Checkpoint: in.Checkpoint,
		Constant:   in.Constant,
		Incumbent:  nullable(in.Incumbent),
		Candidate:  in.Candidate,
		Promoted:   in.Promoted,
		Ts:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return 0, fmt.Errorf("decision: training: insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("decision: training: commit: %w", err)
	}
	return row.Id, nil
}
