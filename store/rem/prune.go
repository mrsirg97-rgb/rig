package rem

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/store"
	remdom "github.com/mrsirg97-rgb/rig/v2/store/rem/domain"
	"github.com/mrsirg97-rgb/rig/v2/store/sqlx"
	"strings"
	"time"
)

type PruneInput struct {
	Verb          string
	IDs           []int64
	Scope         string
	Kind          string
	OlderThanDays int
	Importance    *float64
}

type pruneResult struct {
	reply string
	count int
}

func Prune(ctx context.Context, db store.DB, cwd string, in PruneInput) (string, int, error) {
	if in.Verb == "" {
		return "", 0, fmt.Errorf("rem: prune requires verb remove|reduce|consolidate")
	}
	switch in.Verb {
	case "consolidate":
		res, err := transact(ctx, db, func(bound context.Context, tx *sql.Tx) (pruneResult, error) {
			n, err := consolidatePass(bound, in, cwd)
			if err != nil {
				return pruneResult{}, err
			}
			return pruneResult{reply: fmt.Sprintf("consolidated %d memories", n), count: n}, nil
		})
		if err != nil {
			return "", 0, err
		}
		return res.reply, res.count, nil
	case "remove":
		res, err := transact(ctx, db, func(bound context.Context, tx *sql.Tx) (pruneResult, error) {
			n, err := removeMemories(bound, in, cwd)
			if err != nil {
				return pruneResult{}, err
			}
			return pruneResult{reply: fmt.Sprintf("removed %d", n), count: n}, nil
		})
		if err != nil {
			return "", 0, err
		}
		return res.reply, res.count, nil
	case "reduce":
		res, err := transact(ctx, db, func(bound context.Context, tx *sql.Tx) (pruneResult, error) {
			n, err := reduceImportance(bound, in, cwd)
			if err != nil {
				return pruneResult{}, err
			}
			return pruneResult{reply: fmt.Sprintf("reduced %d to %g", n, *in.Importance), count: n}, nil
		})
		if err != nil {
			return "", 0, err
		}
		return res.reply, res.count, nil
	}
	return "", 0, fmt.Errorf("rem: action '%s' not implemented", in.Verb)
}

func candidatesOf(bound context.Context, in PruneInput, cwd string, whole bool) ([]int64, error) {
	if len(in.IDs) > 0 {
		return dedupeIDs(in.IDs), nil
	}
	hasCriteria := in.Kind != "" || in.OlderThanDays != 0 || in.Scope != ""
	if !hasCriteria {
		if !whole {
			return nil, fmt.Errorf("rem: prune needs ids or criteria (kind/older_than_days/scope)")
		}

		tx, err := sqlx.TxFrom(bound)
		if err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(bound, `SELECT id FROM memories`)
		if err != nil {
			return nil, fmt.Errorf("rem: candidates: %w", err)
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, fmt.Errorf("rem: candidates: %w", err)
			}
			out = append(out, id)
		}
		return out, rows.Err()
	}
	scopes, err := readScopes(in.Scope, cwd)
	if err != nil {
		return nil, err
	}

	clauses := []string{"scope IN (" + placeholders(len(scopes)) + ")"}
	args := make([]any, 0, len(scopes)+2)
	for _, s := range scopes {
		args = append(args, s)
	}
	next := len(scopes) + 1
	if in.Kind != "" {
		clauses = append(clauses, fmt.Sprintf("kind = $%d", next))
		args = append(args, in.Kind)
		next++
	}
	if in.OlderThanDays != 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -in.OlderThanDays).Format(time.RFC3339)
		clauses = append(clauses, fmt.Sprintf("created_at < $%d", next))
		args = append(args, cutoff)
	}
	tx, err := sqlx.TxFrom(bound)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(bound, `SELECT id FROM memories WHERE `+strings.Join(clauses, " AND "), args...)
	if err != nil {
		return nil, fmt.Errorf("rem: candidates: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("rem: candidates: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func dedupeIDs(in []int64) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, id := range in {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func placeholders(n int) string {
	if n == 0 {
		return ""
	}
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if i > 1 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "$%d", i)
	}
	return b.String()
}

func consolidatePass(bound context.Context, in PruneInput, cwd string) (int, error) {
	ids, err := candidatesOf(bound, in, cwd, true)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	rows, err := remdom.NewMemoryDomain().GetMemoryBatch(bound, ids).Rows()
	if err != nil {
		return 0, fmt.Errorf("rem: consolidate: %w", err)
	}
	nowT := time.Now().UTC()
	now := nowT.Format(time.RFC3339)
	for i := range rows {
		next := consolidate(rows[i].Strength, daysSince(rows[i].LastConsolidatedAt, nowT), rows[i].AccessCount, rows[i].Importance)
		rows[i].Strength = next
		rows[i].AccessCount = 0
		rows[i].LastConsolidatedAt = now
		if _, err := remdom.NewMemoryDomain().UpdateMemory(bound, rows[i]); err != nil {
			return 0, fmt.Errorf("rem: consolidate: %w", err)
		}
	}
	return len(rows), nil
}

func daysSince(older string, now time.Time) float64 {
	if older == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, older)
	if err != nil || !t.Before(now) {
		return 0
	}
	return now.Sub(t).Hours() / 24
}

func removeMemories(bound context.Context, in PruneInput, cwd string) (int, error) {
	tx, err := sqlx.TxFrom(bound)
	if err != nil {
		return 0, err
	}
	ids, err := candidatesOf(bound, in, cwd, false)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, id := range ids {

		if _, err := tx.ExecContext(bound, `UPDATE memories SET superseded_by = NULL WHERE superseded_by = $1`, id); err != nil {
			return removed, fmt.Errorf("rem: remove: %w", err)
		}

		if _, err := tx.ExecContext(bound, `DELETE FROM trigrams WHERE memory_id = $1`, id); err != nil {
			return removed, fmt.Errorf("rem: remove: %w", err)
		}
		if ftsEnabled() {

			if _, err := tx.ExecContext(bound, `DELETE FROM memory_fts WHERE rowid = $1`, id); err != nil {
				return removed, fmt.Errorf("rem: remove: %w", err)
			}
		}
		if deleted, err := remdom.NewMemoryDomain().DeleteMemory(bound, id); err == nil && deleted != nil {
			removed++
		} else if err != nil {
			return removed, fmt.Errorf("rem: remove: %w", err)
		}
	}
	return removed, nil
}

func reduceImportance(bound context.Context, in PruneInput, cwd string) (int, error) {
	ids, err := candidatesOf(bound, in, cwd, false)
	if err != nil {
		return 0, err
	}
	if in.Importance == nil {
		return 0, fmt.Errorf("rem: reduce needs an importance to lower to")
	}
	reduced := 0
	for _, id := range ids {
		row, err := remdom.NewMemoryDomain().GetMemory(bound, id).Row()
		if err != nil {
			return reduced, fmt.Errorf("rem: reduce: %w", err)
		}
		if row == nil {
			continue
		}
		row.Importance = *in.Importance
		if _, err := remdom.NewMemoryDomain().UpdateMemory(bound, *row); err != nil {
			return reduced, fmt.Errorf("rem: reduce: %w", err)
		}
		reduced++
	}
	return reduced, nil
}
