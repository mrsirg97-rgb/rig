package state

import (
	"context"
	"errors"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
)

// ErrNoSuchSession marks the projection's unknown-id refusal
// (SPEC_COMMANDS 5): the sessions command builds its voice on it
// (errors.Is), and the resume voice stays as it is
// ("resume: no such session: <id>").
var ErrNoSuchSession = errors.New("no such session")

// SessionRow is one listed session (SPEC_COMMANDS 5): the row's identity
// and lifecycle plus the defined turns count — a turn starts with a user
// prompt, so turns = the session's user rows minus the [compaction]
// summary rows (transcript machinery, not prompts).
type SessionRow struct {
	ID      string
	Started time.Time
	Exit    string // an unclosed row renders as 'open'
	Turns   int
}

// ListSessions is the sessions command's list read (SPEC_COMMANDS 5):
// the workspace's session rows — the file is already workspace-keyed by
// cwd — newest first, capped at 50: a glance, not an archive (show is
// the deep read). An unclosed row (ended_at NULL) renders as 'open' —
// the one place the word appears; the store's exit vocabulary stays
// ok | fault | cancelled.
func ListSessions(ctx context.Context, db store.DB) ([]SessionRow, error) {
	rows, err := db.DB.QueryContext(ctx, `
		SELECT s."id", s."started_at",
			CASE WHEN s."ended_at" IS NULL THEN 'open' ELSE s."exit" END,
			(SELECT count(*) FROM "messages" m
			 WHERE m."session_id" = s."id"
				   AND m."role" = 'user'
				   AND m."content" NOT LIKE '[compaction] %')
		FROM "sessions" s
		ORDER BY s."started_at" DESC
		LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRow
	for rows.Next() {
		var r SessionRow
		if err := rows.Scan(&r.ID, &r.Started, &r.Exit, &r.Turns); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
