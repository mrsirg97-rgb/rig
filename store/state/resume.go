package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/policy/compact"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state/domain"
)

func Resume(ctx context.Context, db store.DB, sessionID string) (*core.Session, error) {
	c, tx, err := db.TxReadOnly(ctx)
	if err != nil {
		return nil, err
	}
	sess, err := resumeIn(c, tx, sessionID)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return sess, nil
}

func resumeIn(ctx context.Context, tx *sql.Tx, sessionID string) (*core.Session, error) {
	s, err := safely(func() (*domain.Session, error) {
		return domain.NewSessionDomain().GetSession(ctx, sessionID).Row()
	})
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("resume: %w: %s", ErrNoSuchSession, sessionID)
	}

	sess := &core.Session{ID: sessionID, Files: map[string]core.FileState{}}

	rows, err := tx.QueryContext(ctx,
		`SELECT "seq", "role", "content", "reasoning"
		 FROM "messages" WHERE "session_id" = $1 ORDER BY "seq"`,
		sessionID)
	if err != nil {
		return nil, fmt.Errorf("resume: %w", err)
	}
	type mrow struct {
		seq       int64
		role      string
		content   string
		reasoning sql.NullString
	}
	var ms []mrow
	for rows.Next() {
		var m mrow
		if err := rows.Scan(&m.seq, &m.role, &m.content, &m.reasoning); err != nil {
			rows.Close()
			return nil, fmt.Errorf("resume: %w", err)
		}
		ms = append(ms, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("resume: %w", err)
	}
	rows.Close()

	start := int64(0)
	for _, m := range ms {
		if m.role == "user" && strings.HasPrefix(m.content, compact.SummaryMarker) {
			start = m.seq
		}
	}

	for _, m := range ms {
		if m.seq < start {
			continue
		}
		switch m.role {
		case "user":
			sess.Append(core.Message{Role: core.RoleUser, Content: m.content})
		case "assistant":
			msg := core.Message{Role: core.RoleAssistant, Content: m.content}
			if m.reasoning.Valid {
				msg.Reasoning = m.reasoning.String
			}
			calls, landed, err := callsFor(ctx, tx, m.seq)
			if err != nil {
				return nil, err
			}
			msg.ToolCalls = calls
			sess.Append(msg)

			for _, l := range landed {
				sess.Append(core.Message{Role: core.RoleTool, ToolID: l.id, Content: l.result})
			}
		default:
			sess.Append(core.Message{Role: core.Role(m.role), Content: m.content})
		}
	}

	files, err := filesFor(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	for p, st := range files {
		sess.Files[p] = st
	}
	return sess, nil
}

type landedResult struct {
	id     string
	result string
}

func callsFor(ctx context.Context, tx *sql.Tx, messageSeq int64) ([]core.ToolCall, []landedResult, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT "id", "name", "args", "result"
		 FROM "tool_calls" WHERE "message_seq" = $1 ORDER BY rowid`,
		messageSeq)
	if err != nil {
		return nil, nil, fmt.Errorf("resume: %w", err)
	}
	defer rows.Close()
	var (
		calls  []core.ToolCall
		landed []landedResult
	)
	for rows.Next() {
		var (
			id, name, args string
			result         sql.NullString
		)
		if err := rows.Scan(&id, &name, &args, &result); err != nil {
			return nil, nil, fmt.Errorf("resume: %w", err)
		}
		calls = append(calls, core.ToolCall{ID: id, Name: name, Args: json.RawMessage(args)})
		if result.Valid {
			landed = append(landed, landedResult{id: id, result: result.String})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("resume: %w", err)
	}
	return calls, landed, nil
}

func filesFor(ctx context.Context, tx *sql.Tx, sessionID string) (map[string]core.FileState, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT "path", "hash", "mtime" FROM "files" WHERE "session_id" = $1`,
		sessionID)
	if err != nil {
		return nil, fmt.Errorf("resume: %w", err)
	}
	defer rows.Close()
	out := map[string]core.FileState{}
	for rows.Next() {
		var (
			path, hash string
			mtime      int64
		)
		if err := rows.Scan(&path, &hash, &mtime); err != nil {
			return nil, fmt.Errorf("resume: %w", err)
		}
		out[path] = core.FileState{Hash: hash, Mtime: mtime}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resume: %w", err)
	}
	return out, nil
}
