package sqlx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type DB struct{ *sql.DB }

type txKey struct{}

const busyWaitMax = 30 * time.Second

const busyBackoff0 = 50 * time.Millisecond

const busyBackoffMax = 500 * time.Millisecond

func (db DB) Tx(ctx context.Context) (context.Context, *sql.Tx, error) {
	return db.beginTx(ctx, false)
}

func (db DB) TxReadOnly(ctx context.Context) (context.Context, *sql.Tx, error) {
	return db.beginTx(ctx, true)
}

func (db DB) beginTx(ctx context.Context, readOnly bool) (context.Context, *sql.Tx, error) {
	opts := &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: readOnly}
	backoff := busyBackoff0
	deadline := time.Now().Add(busyWaitMax)
	for {
		tx, err := db.BeginTx(ctx, opts)
		if err == nil {
			return context.WithValue(ctx, txKey{}, tx), tx, nil
		}
		if !isBusy(err) {
			return nil, nil, err
		}
		if ctx.Err() != nil {
			return nil, nil, fmt.Errorf("sqlx: database busy: %w", ctx.Err())
		}
		if time.Now().After(deadline) {
			return nil, nil, fmt.Errorf("sqlx: database busy after %s: %w", busyWaitMax, err)
		}
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("sqlx: database busy: %w", ctx.Err())
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > busyBackoffMax {
			backoff = busyBackoffMax
		}
	}
}

func isBusy(err error) bool {
	return strings.Contains(err.Error(), "SQLITE_BUSY")
}

func TxFrom(ctx context.Context) (*sql.Tx, error) {
	tx, ok := ctx.Value(txKey{}).(*sql.Tx)
	if !ok {
		return nil, errors.New("sqlx: no transaction bound, call DB.Tx first")
	}
	return tx, nil
}
