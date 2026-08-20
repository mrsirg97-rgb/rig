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

type DB struct{ *sql.DB }

type txKey struct{}

func (db DB) Tx(ctx context.Context) (context.Context, *sql.Tx, error) {
	return db.beginTx(ctx, false)
}

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

func TxFrom(ctx context.Context) (*sql.Tx, error) {
	tx, ok := ctx.Value(txKey{}).(*sql.Tx)
	if !ok {
		return nil, errors.New("sqlx: no transaction bound, call DB.Tx first")
	}
	return tx, nil
}

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
