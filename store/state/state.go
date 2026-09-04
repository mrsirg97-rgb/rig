package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/sqlx"
	"github.com/mrsirg97-rgb/rig/store/state/ddl"
	"github.com/mrsirg97-rgb/rig/store/state/domain"
)

const SchemaVersion = 2

func Migration() func(*sql.Tx, int, int) (string, error) {
	return migrate
}

func migrate(tx *sql.Tx, from, to int) (string, error) {
	var found int
	err := tx.QueryRow(`SELECT 1 FROM pragma_table_info('tool_calls') WHERE name = 'session_id'`).Scan(&found)
	switch {
	case err == nil:
		return "", nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return "", fmt.Errorf("state: migration: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE "tool_calls" ADD COLUMN "session_id" TEXT`); err != nil {
		return "", fmt.Errorf("state: migration: %w", err)
	}
	if _, err := tx.Exec(`UPDATE "tool_calls" SET "session_id" = (
		SELECT "session_id" FROM "messages"
		WHERE "messages"."seq" = "tool_calls"."message_seq"
	)`); err != nil {
		return "", fmt.Errorf("state: migration: %w", err)
	}
	var orphan int
	if err := tx.QueryRow(`SELECT count(*) FROM "tool_calls" WHERE "session_id" IS NULL`).Scan(&orphan); err != nil {
		return "", fmt.Errorf("state: migration: %w", err)
	}
	if orphan > 0 {
		return "", fmt.Errorf("state: migration: %d tool call rows reference no message row", orphan)
	}
	if _, err := tx.Exec(`CREATE TABLE "tool_calls_v2" (
  "session_id" TEXT NOT NULL,
  "message_seq" INTEGER NOT NULL,
  "id" TEXT NOT NULL,
  "args" TEXT NOT NULL,
  "ended_at" TIMESTAMP,
  "err" TEXT,
  "name" TEXT NOT NULL,
  "result" TEXT,
  "started_at" TIMESTAMP NOT NULL,
  PRIMARY KEY ("session_id", "message_seq", "id")
)`); err != nil {
		return "", fmt.Errorf("state: migration: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO "tool_calls_v2" ("session_id", "message_seq", "id", "args", "ended_at", "err", "name", "result", "started_at")
		SELECT "session_id", "message_seq", "id", "args", "ended_at", "err", "name", "result", "started_at" FROM "tool_calls"`); err != nil {
		return "", fmt.Errorf("state: migration: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE "tool_calls"`); err != nil {
		return "", fmt.Errorf("state: migration: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE "tool_calls_v2" RENAME TO "tool_calls"`); err != nil {
		return "", fmt.Errorf("state: migration: %w", err)
	}
	return "state migration: tool_calls keyed by (session_id, message_seq, id)", nil
}

func Statements() []string {
	return ddl.Statements()
}

func RecordSession(ctx context.Context, db store.DB, id, cwd, model, version string) error {
	return withTx(db, ctx, func(c context.Context) error {
		_, err := domain.NewSessionDomain().InsertSession(c, domain.Session{
			Id: id, Cwd: cwd, Model: model, Version: version, StartedAt: now(), Exit: "open",
		})
		return err
	})
}

func RecordMessage(ctx context.Context, db store.DB, sessionID, role, content string, reasoning, toolID *string) (int64, error) {
	var seq int64
	err := withTx(db, ctx, func(c context.Context) error {
		var e error
		seq, e = mintSeq(c, db, "messages", "seq")
		if e != nil {
			return e
		}
		_, e = domain.NewMessageDomain().InsertMessage(c, domain.Message{
			Seq: seq, SessionId: sessionID, Role: role, Content: content,
			Reasoning: reasoning, ToolId: toolID, CreatedAt: now(),
		})
		return e
	})
	return seq, err
}

func CanonicalArgs(args string) (string, error) {
	var v any
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return "", err
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func RecordToolCall(ctx context.Context, db store.DB, sessionID string, messageSeq int64, id, name, args string) error {
	canonical, cerr := CanonicalArgs(args)
	if cerr != nil {
		canonical = args
	}
	err := withTx(db, ctx, func(c context.Context) error {
		_, e := domain.NewToolCallDomain().InsertToolCall(c, domain.ToolCall{
			SessionId: sessionID, Id: id, Name: name, Args: canonical, MessageSeq: messageSeq, StartedAt: now(),
		})
		return e
	})
	if err != nil {
		return err
	}
	return cerr
}

func RecordToolResult(ctx context.Context, db store.DB, sessionID string, messageSeq int64, id, result string, failure *string) error {
	return withTx(db, ctx, func(c context.Context) error {
		tc, err := safely(func() (*domain.ToolCall, error) {
			return domain.NewToolCallDomain().GetToolCall(c, sessionID, messageSeq, id).Row()
		})
		if err != nil {
			return err
		}
		if tc == nil {
			return fmt.Errorf("state: tool call %s (session %s, message %d): no such row", id, sessionID, messageSeq)
		}
		tc.Result = &result
		t := now()
		tc.EndedAt = &t
		tc.Err = failure
		_, err = domain.NewToolCallDomain().UpdateToolCall(c, *tc)
		return err
	})
}

func RecordUsage(ctx context.Context, db store.DB, messageSeq, prompt, completion, cacheRead, cacheWrite int64) error {
	return withTx(db, ctx, func(c context.Context) error {
		_, err := domain.NewUsageDomain().InsertUsage(c, domain.Usage{
			MessageSeq: messageSeq, Prompt: prompt, Completion: completion,
			CacheRead: cacheRead, CacheWrite: cacheWrite,
		})
		return err
	})
}

func RecordFile(ctx context.Context, db store.DB, sessionID, path, hash string, mtime int64) error {
	return withTx(db, ctx, func(c context.Context) error {
		_, err := domain.NewFileDomain().InsertFile(c, domain.File{
			SessionId: sessionID, Path: path, Hash: hash, Mtime: mtime,
		})
		return err
	})
}

func RecordFault(ctx context.Context, db store.DB, sessionID string, at time.Time, message string) (int64, error) {
	var seq int64
	err := withTx(db, ctx, func(c context.Context) error {
		var e error
		seq, e = mintSeq(c, db, "faults", "seq")
		if e != nil {
			return e
		}
		_, e = domain.NewFaultDomain().InsertFault(c, domain.Fault{
			Seq: seq, SessionId: sessionID, At: at, Message: message,
		})
		return e
	})
	return seq, err
}

func CloseSession(ctx context.Context, db store.DB, id, exit string) error {
	return withTx(db, ctx, func(c context.Context) error {
		s, err := safely(func() (*domain.Session, error) {
			return domain.NewSessionDomain().GetSession(c, id).Row()
		})
		if err != nil {
			return err
		}
		if s == nil {
			return fmt.Errorf("state: session %s: no such row", id)
		}
		t := now()
		s.EndedAt = &t
		s.Exit = exit
		_, err = domain.NewSessionDomain().UpdateSession(c, *s)
		return err
	})
}

func now() time.Time { return time.Now().UTC() }

func safely[T any](fn func() (T, error)) (out T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("state: %v", r)
		}
	}()
	return fn()
}

func withTx(db store.DB, ctx context.Context, fn func(context.Context) error) error {
	if _, err := sqlx.TxFrom(ctx); err == nil {
		return fn(ctx)
	}
	c, tx, err := db.Tx(ctx)
	if err != nil {
		return err
	}
	if err := fn(c); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func mintSeq(ctx context.Context, db store.DB, table, column string) (int64, error) {
	var seq int64
	err := db.DB.QueryRowContext(ctx, "SELECT COALESCE(MAX("+column+"), 0) + 1 FROM "+table).Scan(&seq)
	return seq, err
}
