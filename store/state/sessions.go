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

// Observation is one recorded tool call row, as the diff tool's last
// verb reads it (SPEC_DIFF): the result, when the call started, and the
// message seq the call is attributed to.
type Observation struct {
	Result    string
	StartedAt time.Time // RFC3339 UTC, as stored
	Seq       int64     // the row's message_seq
}

// RecentToolCalls is the diff tool's last verb's pair read
// (SPEC_DIFF): the n+1 most recent completed calls of (name, canonical
// args) in sessionID, newest first; fewer than n+1 means no earlier
// observation at the tool layer. Completed means result IS NOT NULL:
// the in-flight call's result has not landed. The args match is the
// exact string equality decision 3's write-time canonicalization
// makes safe. The order is total: started_at is second precision,
// message_seq breaks ties across messages, id breaks ties within one
// (a multi-call message in a single second). The world boundary
// (SPEC_DIFF 5) is the session's last [compaction] marker row: the
// re-landed tail is in scope, the rows before it are another world.
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
