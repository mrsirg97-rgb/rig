// Package sqlx is the stdlib sql seam the generated stack executes
// through: a DB wraps the pool, and Tx opens the serializable
// transaction that rides the context. sqlite and postgres both speak
// database/sql (postgres through the pgx/v5/stdlib driver), so the
// cameras never learn which — one constructor, one isolation, one
// read of the context.
package sqlx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// DB wraps the stdlib pool: one transaction constructor. the embedded
// *sql.DB carries the driver; the generated stack only ever sees the
// serializable tx it opens.
type DB struct{ *sql.DB }

// txKey is the typed, unexported context key the transaction rides on —
// a unique type, not a string, so no other value can collide with it.
type txKey struct{}

// Tx opens the serializable transaction and lands it on the returned
// context: every generated accessor reads the tx off the ctx, so the
// caller's choice of pool-vs-tx is made here, once, per request. this
// is the read-write constructor — the mesh fold and the write routes.
func (db DB) Tx(ctx context.Context) (context.Context, *sql.Tx, error) {
	return db.beginTx(ctx, false)
}

// TxReadOnly opens the serializable read-only transaction: postgres
// skips the SSI predicate-lock bookkeeping for a READ ONLY serializable
// snapshot, so a read board draws nothing from the shared-memory lock
// table. GET routes take this constructor; writes keep Tx. same typed
// key on the context, so the accessors cannot tell the difference.
func (db DB) TxReadOnly(ctx context.Context) (context.Context, *sql.Tx, error) {
	return db.beginTx(ctx, true)
}

func (db DB) beginTx(ctx context.Context, readOnly bool) (context.Context, *sql.Tx, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: readOnly})
	if err != nil {
		return nil, nil, err
	}
	return context.WithValue(ctx, txKey{}, tx), tx, nil
}

// TxFrom resolves the bound transaction: the one read of the context the
// generated cameras make. absent refuses loudly — the stack fails closed
// on an unbound request.
func TxFrom(ctx context.Context) (*sql.Tx, error) {
	tx, ok := ctx.Value(txKey{}).(*sql.Tx)
	if !ok {
		return nil, errors.New("sqlx: no transaction bound, call DB.Tx first")
	}
	return tx, nil
}

// ArrayScanner adapts a []T destination so database/sql can fill it.
// drivers deliver array columns as text: pgx/v5/stdlib hands postgres
// arrays as the array literal ({"a","b"}), and TEXT-backed sqlite
// arrays as JSON (["a","b"]). a destination that implements
// sql.Scanner lets the scan fill the slice — the generated domain Scan
// passes the adapter straight through.
//
//	scan row.Scan((*sqlx.ArrayScanner[string])(&out.Tags))

type ArrayScanner[T any] []T

func (s *ArrayScanner[T]) Scan(src any) error {
	var text string
	var ok bool
	switch v := src.(type) {
	case string:
		text, ok = v, true
	case []byte:
		text, ok = string(v), true
	case nil:
		*s = nil
		return nil
	}
	if !ok {
		return errors.New("sqlx: array column as " + fmt.Sprint(src))
	}
	switch {
	case strings.HasPrefix(text, "{"):
		parsed, err := parseArrayLiteral(text)
		if err != nil {
			return err
		}
		return fillSlice(s, parsed)
	case strings.HasPrefix(text, "["):
		var decoded []string
		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			return err
		}
		return fillSlice(s, decoded)
	default:
		return errors.New("sqlx: unrecognized array text " + text)
	}
}

// fillSlice converts each parsed element to T and lands the slice. the
// conversion switch is the price of a single generic adapter; slice
// columns are rare and this runs once per scanned row.
func fillSlice[T any](dst *ArrayScanner[T], elems []string) error {
	out := make([]T, 0, len(elems))
	var zero T
	switch any(zero).(type) {
	case string:
		for _, e := range elems {
			out = append(out, any(e).(T))
		}
	case int32:
		for _, e := range elems {
			v, err := strconv.Atoi(strings.TrimSpace(e))
			if err != nil {
				return err
			}
			out = append(out, any(int32(v)).(T))
		}
	case int64:
		for _, e := range elems {
			v, err := strconv.Atoi(strings.TrimSpace(e))
			if err != nil {
				return err
			}
			out = append(out, any(int64(v)).(T))
		}
	case int16:
		for _, e := range elems {
			v, err := strconv.Atoi(strings.TrimSpace(e))
			if err != nil {
				return err
			}
			out = append(out, any(int16(v)).(T))
		}
	case float64:
		for _, e := range elems {
			v, err := strconv.ParseFloat(strings.TrimSpace(e), 64)
			if err != nil {
				return err
			}
			out = append(out, any(v).(T))
		}
	case bool:
		for _, e := range elems {
			v, err := strconv.ParseBool(strings.TrimSpace(e))
			if err != nil {
				return err
			}
			out = append(out, any(v).(T))
		}
	default:
		return errors.New("sqlx: unsupported array element type")
	}
	*dst = out
	return nil
}

// parseArrayLiteral splits a postgres array literal into unquoted
// elements, handling quoted strings and embedded commas/escapes.
func parseArrayLiteral(text string) ([]string, error) {
	if !strings.HasPrefix(text, "{") || !strings.HasSuffix(text, "}") {
		return nil, errors.New("sqlx: malformed array literal " + text)
	}
	body := text[1 : len(text)-1]
	var out []string
	cur := ""
	quoted := false
	escaped := false
	for _, r := range body {
		switch {
		case escaped:
			cur += string(r)
			escaped = false
		case r == '\\':
			cur += string(r)
			escaped = true
			continue
		case r == '"':
			quoted = !quoted
			continue
		case r == ',' && !quoted:
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if escaped {
		return nil, errors.New("sqlx: unterminated escape in array literal")
	}
	out = append(out, cur)
	return out, nil
}
