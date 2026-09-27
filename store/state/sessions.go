package state

import (
	"context"
	"errors"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
)

var ErrNoSuchSession = errors.New("no such session")

const ListCap = 50

type SessionRow struct {
	ID      string
	Cwd     string
	Model   string
	Version string
	Started time.Time
	Exit    string
	Turns   int
	Faults  int
	Tokens  int64
	Cost    float64
	Label   string
}

func ListSessions(ctx context.Context, db store.DB, n int) ([]SessionRow, error) {
	if n <= 0 || n > ListCap {
		n = ListCap
	}
	// the n newest sessions are chosen first; the aggregates run for
	// those n only (a LIMIT after the select list would compute them for
	// every session in the store).
	rows, err := db.DB.QueryContext(ctx, `
		WITH s AS (SELECT * FROM "sessions" ORDER BY "started_at" DESC LIMIT $1)
		SELECT s."id", s."cwd", s."started_at",
			CASE WHEN s."ended_at" IS NULL THEN 'open' ELSE s."exit" END,
			s."model", s."version",
			(SELECT count(*) FROM "messages" m
			 WHERE m."session_id" = s."id"
				   AND m."role" = 'user'
				   AND m."seq" > COALESCE((
				         SELECT MAX(m2."seq") FROM "messages" m2
				         WHERE m2."session_id" = s."id"
				           AND m2."role" = 'user'
				           AND m2."content" LIKE '[compaction] %'
				       ), 0)),
			(SELECT count(*) FROM "faults" f WHERE f."session_id" = s."id"),
			COALESCE((SELECT SUM(u."prompt" + u."completion") FROM "usage" u
				  JOIN "messages" m ON m."seq" = u."message_seq"
				  WHERE m."session_id" = s."id"), 0),
			COALESCE((SELECT SUM(u."cost") FROM "usage" u
				  JOIN "messages" m ON m."seq" = u."message_seq"
				  WHERE m."session_id" = s."id"), 0),
			COALESCE(s."label", '')
		FROM s
		ORDER BY s."started_at" DESC`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRow
	for rows.Next() {
		var r SessionRow
		if err := rows.Scan(&r.ID, &r.Cwd, &r.Started, &r.Exit, &r.Model, &r.Version, &r.Turns, &r.Faults, &r.Tokens, &r.Cost, &r.Label); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type Observation struct {
	Result    string
	StartedAt time.Time
	Seq       int64
}

func RecentToolCalls(ctx context.Context, db store.DB, sessionID, name, args string, n int) ([]Observation, error) {
	rows, err := db.DB.QueryContext(ctx, `
		SELECT tc."result", tc."started_at", tc."message_seq"
		FROM "tool_calls" tc
		JOIN "messages" m ON m."seq" = tc."message_seq"
		WHERE m."session_id" = $1
		  AND tc."name" = $2
		  AND tc."args" = $3
		  AND tc."result" IS NOT NULL
		  AND tc."message_seq" > COALESCE((
		        SELECT MAX(m2."seq") FROM "messages" m2
		        WHERE m2."session_id" = $1
		          AND m2."role" = 'user'
		          AND m2."content" LIKE '[compaction] %'
		      ), 0)
		ORDER BY tc."started_at" DESC, tc."message_seq" DESC, tc."id" DESC
		LIMIT $4`, sessionID, name, args, n+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Observation
	for rows.Next() {
		var o Observation
		if err := rows.Scan(&o.Result, &o.StartedAt, &o.Seq); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Cwds is the distinct working directories a store holds sessions for:
// the dashboard's workspace list, one tiny read per store instead of the
// full session listing.
func Cwds(ctx context.Context, db store.DB) ([]string, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT DISTINCT "cwd" FROM "sessions" WHERE "cwd" <> '' ORDER BY "cwd"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cwd string
		if err := rows.Scan(&cwd); err != nil {
			return nil, err
		}
		out = append(out, cwd)
	}
	return out, rows.Err()
}
