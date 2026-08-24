package state

import (
	"context"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
)

type FaultRow struct {
	Seq       int64
	SessionID string
	At        time.Time
	Message   string
}

func SessionFaults(ctx context.Context, db store.DB, sessionID string) ([]FaultRow, error) {
	rows, err := db.DB.QueryContext(ctx, `
		SELECT "seq", "session_id", "at", "message"
		FROM "faults"
		WHERE "session_id" = $1
		ORDER BY "at" DESC, "seq" DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FaultRow
	for rows.Next() {
		var r FaultRow
		if err := rows.Scan(&r.Seq, &r.SessionID, &r.At, &r.Message); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
