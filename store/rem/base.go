package rem

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/store"
	remdd "github.com/mrsirg97-rgb/rig/v2/store/rem/ddl"
	remdom "github.com/mrsirg97-rgb/rig/v2/store/rem/domain"
	remmeta "github.com/mrsirg97-rgb/rig/v2/store/rem/metadata"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

const SchemaVersion = 4

func DDL() []string { return remdd.Statements() }

func Statements() []string {
	out := remdd.Statements()
	out = append(out, remmeta.ExtraStatements()...)
	return append(out, remmeta.FtsStatements()...)
}

var (
	ftsOverrideSet atomic.Bool
	ftsOverride    atomic.Bool
)

func SetFtsAvailable(v *bool) {
	if v == nil {
		ftsOverrideSet.Store(false)
		return
	}
	ftsOverride.Store(*v)
	ftsOverrideSet.Store(true)
}

func ftsEnabled() bool {
	if ftsOverrideSet.Load() {
		return ftsOverride.Load()
	}
	return true
}

func scopeKey(cwd string) string {
	return scope.Key(cwd)
}

func writeScope(scope, cwd string) (key, label string, err error) {
	if scope == "global" {
		return "global", "global", nil
	}
	if scope != "" && scope != "project" {
		return "", "", fmt.Errorf("rem: scope must be project or global, got '%s'", scope)
	}
	label = filepath.Base(cwd)
	if label == "." || label == "" {
		label = "root"
	}
	return scopeKey(cwd), label, nil
}

func readScopes(scope, cwd string) ([]string, error) {
	switch scope {
	case "global":
		return []string{"global"}, nil
	case "all":
		return []string{scopeKey(cwd), "global"}, nil
	case "", "project":
		return []string{scopeKey(cwd)}, nil
	}
	return nil, fmt.Errorf("rem: scope must be project, global, or all")
}

func sha256hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func zero[T any]() T { var z T; return z }

func transact[T any](ctx context.Context, db store.DB, act func(bound context.Context, tx *sql.Tx) (T, error)) (T, error) {
	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return zero[T](), err
	}
	defer tx.Rollback()
	out, err := act(bound, tx)
	if err != nil {
		return zero[T](), err
	}
	if err := tx.Commit(); err != nil {
		return zero[T](), err
	}
	return out, nil
}

const idCounterKey = "memory_id_seq"

func mintID(bound context.Context) (int64, error) {
	existing, err := remdom.NewMetaDomain().GetMeta(bound, idCounterKey).Row()
	if err != nil {
		return 0, fmt.Errorf("rem: id counter: %w", err)
	}
	var next int64
	if existing != nil {
		if next, err = strconv.ParseInt(existing.Value, 10, 64); err != nil {
			return 0, fmt.Errorf("rem: id counter: %w", err)
		}
	}
	next++
	val := fmt.Sprint(next)
	switch {
	case existing == nil:
		if _, err := remdom.NewMetaDomain().InsertMeta(bound, remdom.Meta{Key: idCounterKey, Value: val}); err != nil {
			return 0, fmt.Errorf("rem: id counter: %w", err)
		}
	default:
		if _, err := remdom.NewMetaDomain().UpdateMeta(bound, remdom.Meta{Key: idCounterKey, Value: val}); err != nil {
			return 0, fmt.Errorf("rem: id counter: %w", err)
		}
	}
	return next, nil
}

func applySupersedes(bound context.Context, byID int64, targets []int64, callerScope string) error {
	seen := map[int64]bool{}
	var uniq []int64
	for _, id := range targets {
		if id == byID || seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	for _, id := range uniq {
		row, err := remdom.NewMemoryDomain().GetMemory(bound, id).Row()
		if err != nil {
			return fmt.Errorf("rem: supersedes: %w", err)
		}
		if row == nil {
			return fmt.Errorf("rem: supersedes target m%d not found", id)
		}
		if row.Scope != "global" && row.Scope != callerScope {
			return fmt.Errorf("%w: m%d is %s's; supersede it from there", ErrOtherProject, id, row.ScopeLabel)
		}
		row.SupersededBy = &byID
		if _, err := remdom.NewMemoryDomain().UpdateMemory(bound, *row); err != nil {
			return fmt.Errorf("rem: supersedes: %w", err)
		}
	}
	return nil
}
