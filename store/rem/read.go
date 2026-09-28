package rem

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/store"
	remdom "github.com/mrsirg97-rgb/rig/v2/store/rem/domain"
	"github.com/mrsirg97-rgb/rig/v2/store/sqlx"
)

var ErrNoSuchMemory = errors.New("rem: no such memory")

var ErrOtherProject = errors.New("rem: another project's memory")

type LiveMemory struct {
	ID         int64
	ScopeLabel string
	Kind       string
	Content    string
	CreatedAt  string
	Strength   float64
}

func List(ctx context.Context, db store.DB, cwd string, k int) ([]LiveMemory, error) {
	if k <= 0 {
		k = 50
	}
	proj := scopeKey(cwd)
	out, err := transact(ctx, db, func(bound context.Context, _ *sql.Tx) ([]LiveMemory, error) {
		tx, err := sqlx.TxFrom(bound)
		if err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(bound,
			`SELECT id, scope_label, kind, content, created_at, strength FROM memories
			 WHERE superseded_by IS NULL AND scope IN ($1, $2)
			 ORDER BY CASE WHEN scope = $1 THEN 0 ELSE 1 END, id DESC LIMIT $3`,
			proj, "global", k)
		if err != nil {
			return nil, fmt.Errorf("rem: list: %w", err)
		}
		defer rows.Close()
		var res []LiveMemory
		for rows.Next() {
			var m LiveMemory
			if err := rows.Scan(&m.ID, &m.ScopeLabel, &m.Kind, &m.Content, &m.CreatedAt, &m.Strength); err != nil {
				return nil, fmt.Errorf("rem: list: %w", err)
			}
			res = append(res, m)
		}
		return res, rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func Show(ctx context.Context, db store.DB, id int64) (*remdom.Memory, error) {
	out, err := transact(ctx, db, func(bound context.Context, _ *sql.Tx) (*remdom.Memory, error) {
		row, err := remdom.NewMemoryDomain().GetMemory(bound, id).Row()
		if err != nil {
			return nil, fmt.Errorf("rem: show: %w", err)
		}
		if row == nil {
			return nil, ErrNoSuchMemory
		}
		return row, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func Forget(ctx context.Context, db store.DB, cwd string, id int64) error {
	_, err := transact(ctx, db, func(bound context.Context, tx *sql.Tx) (struct{}, error) {
		row, err := remdom.NewMemoryDomain().GetMemory(bound, id).Row()
		if err != nil {
			return struct{}{}, fmt.Errorf("rem: forget: %w", err)
		}
		if row == nil {
			return struct{}{}, ErrNoSuchMemory
		}
		if row.Scope != "global" && row.Scope != scopeKey(cwd) {
			return struct{}{}, fmt.Errorf("%w: m%d is %s's; forget it from there", ErrOtherProject, id, row.ScopeLabel)
		}
		if _, err := removeMemories(bound, PruneInput{Verb: "remove", IDs: []int64{id}}, ""); err != nil {
			return struct{}{}, fmt.Errorf("rem: forget: %w", err)
		}
		return struct{}{}, nil
	})
	return err
}
