package state

import (
	"context"
	"fmt"
	"time"

	"github.com/mrsirg97-rgb/looper/store"
	"github.com/mrsirg97-rgb/looper/store/sqlx"
	"github.com/mrsirg97-rgb/looper/store/state/ddl"
	"github.com/mrsirg97-rgb/looper/store/state/domain"
)

// SchemaVersion is the schema version this build applies. A bump is a
// named change; Open refuses mismatches loudly.
const SchemaVersion = 1

//go:generate GOGEN=$PWD; cd ../../../lift/cmd && go run main.go -config=$GOGEN/gen.json -source=$GOGEN/source.json

// Statements is the store's schema in application order: the generated
// DDL, then any extra.sql hand-written beside it. state has no extra.sql.
func Statements() []string {
	return ddl.Statements()
}

// record/append functions: each event lands in its own short transaction —
// one inside the caller's bound transaction if one exists, otherwise
// opened here. A kill mid-turn leaves every earlier committed row
// readable. Outside any transaction they never write.

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

func RecordToolCall(ctx context.Context, db store.DB, messageSeq int64, id, name, args string) error {
	return withTx(db, ctx, func(c context.Context) error {
		_, err := domain.NewToolCallDomain().InsertToolCall(c, domain.ToolCall{
			Id: id, Name: name, Args: args, MessageSeq: messageSeq, StartedAt: now(),
		})
		return err
	})
}

func RecordToolResult(ctx context.Context, db store.DB, id, result string, failure *string) error {
	return withTx(db, ctx, func(c context.Context) error {
		tc, err := safely(func() (*domain.ToolCall, error) {
			return domain.NewToolCallDomain().GetToolCall(c, id).Row()
		})
		if err != nil {
			return err
		}
		if tc == nil {
			return fmt.Errorf("state: tool call %s: no such row", id)
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

// safely recovers the generated getters' empty-key panic at this boundary
// into a loud error: they panic rather than erring, and a store never
// panics.
func safely[T any](fn func() (T, error)) (out T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("state: %v", r)
		}
	}()
	return fn()
}

// withTx runs fn inside the caller's bound transaction when one exists,
// otherwise in a short transaction opened here: one event, one transaction,
// committed on success, rolled back on any error.
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

// mintSeq is the store's one raw query: primary-key minting (max+1) inside
// the bound transaction. The generated Insert takes the key from the
// caller, and ids are minted by the store — never invented by a model.
func mintSeq(ctx context.Context, db store.DB, table, column string) (int64, error) {
	var seq int64
	err := db.DB.QueryRowContext(ctx, "SELECT COALESCE(MAX("+column+"), 0) + 1 FROM "+table).Scan(&seq)
	return seq, err
}
