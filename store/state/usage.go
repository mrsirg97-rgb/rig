package state

import (
	"context"

	"github.com/mrsirg97-rgb/rig/store"
)

type UsageRow struct {
	Seq        int64
	Prompt     int64
	Completion int64
	CacheRead  int64
	CacheWrite int64
	Cost       float64
}

func SessionUsage(ctx context.Context, db store.DB, sessionID string) ([]UsageRow, error) {
	rows, err := db.DB.QueryContext(ctx, `
		SELECT u."message_seq", u."prompt", u."completion", u."cache_read", u."cache_write", u."cost"
		FROM "usage" u
		JOIN "messages" m ON m."seq" = u."message_seq"
		WHERE m."session_id" = ?
		ORDER BY u."message_seq"`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var r UsageRow
		if err := rows.Scan(&r.Seq, &r.Prompt, &r.Completion, &r.CacheRead, &r.CacheWrite, &r.Cost); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func SessionCost(ctx context.Context, db store.DB, sessionID string) (float64, error) {
	var cost float64
	err := db.DB.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(u."cost"), 0)
		FROM "usage" u
		JOIN "messages" m ON m."seq" = u."message_seq"
		WHERE m."session_id" = ?`, sessionID).Scan(&cost)
	if err != nil {
		return 0, err
	}
	return cost, nil
}
