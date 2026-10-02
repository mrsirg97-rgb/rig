package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisiondomain "github.com/mrsirg97-rgb/rig/v2/store/decision/domain"
)

const stateCap = 4096

type FinalInput struct {
	Scope    string
	Session  string
	Site     string
	State    string
	Question decision.Question
	Answer   string
	Decider  string
}

type ProposeInput struct {
	Scope      string
	Session    string
	Site       string
	State      string
	Question   decision.Question
	Answer     string
	Confidence float64
	Decider    string
}

type PendingRow struct {
	ID         int64
	Scope      string
	Site       string
	State      string
	Question   decision.Question
	Answer     string
	Confidence *float64
	Decider    string
	Session    string
	Ts         string
}

type SettleInput struct {
	ID             int64
	Approved       bool
	Reviewer       string
	ReviewerAnswer string
}

func RecordFinal(ctx context.Context, db store.DB, in FinalInput) (int64, error) {
	return insert(ctx, db, row{
		scope: in.Scope, session: in.Session, site: in.Site, state: in.State,
		question: in.Question, answer: in.Answer, confidence: nil,
		status: decision.StatusFinal, decider: in.Decider,
	})
}

func Propose(ctx context.Context, db store.DB, in ProposeInput) (int64, error) {
	if in.Confidence < 0 || in.Confidence > 1 {
		return 0, fmt.Errorf("decision: confidence %g is not a probability", in.Confidence)
	}
	return insert(ctx, db, row{
		scope: in.Scope, session: in.Session, site: in.Site, state: in.State,
		question: in.Question, answer: in.Answer, confidence: &in.Confidence,
		status: decision.StatusPending, decider: in.Decider,
	})
}

type row struct {
	scope      string
	session    string
	site       string
	state      string
	question   decision.Question
	answer     string
	confidence *float64
	status     string
	decider    string
}

func insert(ctx context.Context, db store.DB, r row) (int64, error) {
	if r.scope == "" {
		return 0, fmt.Errorf("decision: no scope")
	}
	if r.site == "" {
		return 0, fmt.Errorf("decision: no site")
	}
	if r.decider == "" {
		return 0, fmt.Errorf("decision: no decider")
	}
	qjson, err := json.Marshal(r.question)
	if err != nil {
		return 0, fmt.Errorf("decision: question: %w", err)
	}
	if len(r.state) > stateCap {
		r.state = truncate(r.state, stateCap)
	}
	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX("id"), 0) + 1 FROM "decisions"`).Scan(&id); err != nil {
		return 0, fmt.Errorf("decision: mint id: %w", err)
	}
	out, err := decisiondomain.NewDecisionDomain().InsertDecision(bound, decisiondomain.Decision{
		Id:             id,
		Scope:          r.scope,
		Site:           r.site,
		State:          r.state,
		Question:       string(qjson),
		Answer:         r.answer,
		Confidence:     r.confidence,
		Decider:        r.decider,
		Session:        nullable(r.session),
		Status:         r.status,
		ReviewerAnswer: nil,
		Ts:             time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return 0, fmt.Errorf("decision: insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("decision: commit: %w", err)
	}
	return out.Id, nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func truncate(s string, cap int) string {
	if cap >= len(s) {
		return s
	}
	for cap > 0 && !utf8.RuneStart(s[cap]) {
		cap--
	}
	return s[:cap]
}

func Pending(ctx context.Context, db store.DB) ([]PendingRow, error) {
	_, tx, err := db.TxReadOnly(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT "id", "scope", "site", "state", "question", "answer", "confidence", "decider", "session", "ts"
		 FROM "decisions" WHERE "status" = 'pending' ORDER BY "id"`)
	if err != nil {
		return nil, fmt.Errorf("decision: pending: %w", err)
	}
	defer rows.Close()
	var out []PendingRow
	for rows.Next() {
		var r PendingRow
		var session *string
		var qjson string
		if err := rows.Scan(&r.ID, &r.Scope, &r.Site, &r.State, &qjson, &r.Answer, &r.Confidence, &r.Decider, &session, &r.Ts); err != nil {
			return nil, fmt.Errorf("decision: pending scan: %w", err)
		}
		if session != nil {
			r.Session = *session
		}
		if err := json.Unmarshal([]byte(qjson), &r.Question); err != nil {
			continue
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("decision: pending: %w", err)
	}
	return out, nil
}

func Settle(ctx context.Context, db store.DB, in SettleInput) error {
	_, tx, err := db.Tx(ctx)
	if err != nil {
		return fmt.Errorf("decision: settle: %w", err)
	}
	defer tx.Rollback()
	status := decision.StatusDenied
	answer := nullable(in.ReviewerAnswer)
	if in.Approved {
		status = decision.StatusApproved
		answer = nil
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE "decisions" SET "status" = ?, "reviewer" = ?, "reviewer_answer" = ? WHERE "id" = ? AND "status" = 'pending'`,
		status, nullable(in.Reviewer), answer, in.ID)
	if err != nil {
		return fmt.Errorf("decision: settle: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("decision: settle commit: %w", err)
	}
	return nil
}

func Outcome(ctx context.Context, db store.DB, id int64, outcome string) error {
	_, tx, err := db.Tx(ctx)
	if err != nil {
		return fmt.Errorf("decision: outcome: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE "decisions" SET "outcome" = ? WHERE "id" = ?`, nullable(outcome), id)
	if err != nil {
		return fmt.Errorf("decision: outcome: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("decision: no decision %d", id)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("decision: outcome commit: %w", err)
	}
	return nil
}
