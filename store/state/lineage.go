package state

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mrsirg97-rgb/rig/v3/store"
)

// TrailingReasoning is the reasoning of a session's trailing run of assistant turns of one model: the last assistant
// turn's model, back to the turn before the last switch (SPEC_LINEAGE) - when the session was last on `row` (its
// sessions.model), else none: the run is another row's. A resumed session seeds its row's own record with it, so the
// request after a resume sends what the one before it sent.
func TrailingReasoning(ctx context.Context, db store.DB, sessionID, row string) ([]string, error) {
	c, tx, err := db.TxReadOnly(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var last sql.NullString
	if err := tx.QueryRowContext(c, `SELECT "model" FROM "sessions" WHERE "id" = $1`, sessionID).Scan(&last); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("lineage: %w", err)
	}
	if !last.Valid || last.String != row {
		return nil, nil
	}
	rows, err := tx.QueryContext(c,
		`SELECT "reasoning", "model" FROM "messages"
		 WHERE "session_id" = $1 AND "role" = 'assistant' ORDER BY "seq" DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("lineage: %w", err)
	}
	defer rows.Close()
	var (
		out   []string
		model sql.NullString
		first = true
	)
	for rows.Next() {
		var reasoning, m sql.NullString
		if err := rows.Scan(&reasoning, &m); err != nil {
			return nil, fmt.Errorf("lineage: %w", err)
		}
		if first {
			model, first = m, false
		} else if m != model {
			break
		}
		if reasoning.Valid && reasoning.String != "" {
			out = append([]string{reasoning.String}, out...)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lineage: %w", err)
	}
	return out, nil
}
