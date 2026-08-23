package rem

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	remdd "github.com/mrsirg97-rgb/rig/store/rem/ddl"
	remdom "github.com/mrsirg97-rgb/rig/store/rem/domain"
	remmeta "github.com/mrsirg97-rgb/rig/store/rem/metadata"
	"github.com/mrsirg97-rgb/rig/store/scope"
	"github.com/mrsirg97-rgb/rig/store/sqlx"
)

const SchemaVersion = 2

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

func md5hex(content string) string {
	sum := md5.Sum([]byte(content))
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

func applySupersedes(bound context.Context, byID int64, targets []int64) error {
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
		row.SupersededBy = &byID
		if _, err := remdom.NewMemoryDomain().UpdateMemory(bound, *row); err != nil {
			return fmt.Errorf("rem: supersedes: %w", err)
		}
	}
	return nil
}

type LearnInput struct {
	Content       string
	Kind          string
	Importance    float64
	ImportanceSet bool
	Scope         string
	Source        string
	Supersedes    []int64
}

type ReflectInput struct {
	Content       string
	Importance    float64
	ImportanceSet bool
	Scope         string
	Source        string
}

type writeResult struct {
	reply    string
	mem      *remdom.Memory
	existing bool
}

func Learn(ctx context.Context, db store.DB, cwd string, in LearnInput) (string, *remdom.Memory, bool, error) {
	if in.Content == "" {
		return "", nil, false, fmt.Errorf("rem: action 'learn' requires content")
	}
	res, err := transact(ctx, db, func(bound context.Context, tx *sql.Tx) (writeResult, error) {
		mem, existing, err := storeOrTouch(bound, writeShape{
			content:       in.Content,
			kind:          in.Kind,
			importance:    in.Importance,
			importanceSet: in.ImportanceSet,
			source:        in.Source,
			scope:         in.Scope,
			supersedes:    in.Supersedes,
		}, cwd)
		if err != nil {
			return writeResult{}, err
		}
		if existing {
			note := fmt.Sprintf("already known m%d", mem.Id)
			if in.ImportanceSet {
				note += fmt.Sprintf(" · importance → %g", in.Importance)
			}
			return writeResult{reply: note, mem: mem, existing: true}, nil
		}
		return writeResult{reply: fmt.Sprintf("learned m%d (%s · %s · %g)", mem.Id, mem.ScopeLabel, mem.Kind, mem.Importance), mem: mem}, nil
	})
	if err != nil {
		return "", nil, false, err
	}
	return res.reply, res.mem, res.existing, nil
}

func Reflect(ctx context.Context, db store.DB, cwd string, in ReflectInput) (string, *remdom.Memory, bool, error) {
	if in.Content == "" {
		return "", nil, false, fmt.Errorf("rem: action 'reflect' requires content")
	}
	res, err := transact(ctx, db, func(bound context.Context, tx *sql.Tx) (writeResult, error) {
		mem, existing, err := storeOrTouch(bound, writeShape{
			content:       in.Content,
			kind:          kindReflection,
			importance:    in.Importance,
			importanceSet: in.ImportanceSet,
			source:        in.Source,
			sourceSet:     in.Source != "",
			scope:         in.Scope,
		}, cwd)
		if err != nil {
			return writeResult{}, err
		}
		if existing {
			note := fmt.Sprintf("already known m%d", mem.Id)
			if in.ImportanceSet {
				note += fmt.Sprintf(" · importance → %g", in.Importance)
			}
			if in.Source != "" {
				note += " · source updated"
			}
			return writeResult{reply: note, mem: mem, existing: true}, nil
		}
		return writeResult{reply: fmt.Sprintf("reflected m%d (%s · %s · %g)", mem.Id, mem.ScopeLabel, mem.Kind, mem.Importance), mem: mem}, nil
	})
	if err != nil {
		return "", nil, false, err
	}
	return res.reply, res.mem, res.existing, nil
}

type writeShape struct {
	content       string
	kind          string
	importance    float64
	importanceSet bool
	source        string
	sourceSet     bool
	scope         string
	supersedes    []int64
}

func storeOrTouch(bound context.Context, sh writeShape, cwd string) (*remdom.Memory, bool, error) {
	scopeKey, scopeLabel, err := writeScope(sh.scope, cwd)
	if err != nil {
		return nil, false, err
	}
	digest := md5hex(sh.content)

	tx, err := sqlx.TxFrom(bound)
	if err != nil {
		return nil, false, err
	}
	var existingID int64
	err = tx.QueryRowContext(bound, `SELECT id FROM memories WHERE scope = $1 AND content_md5 = $2`, scopeKey, digest).Scan(&existingID)
	switch {
	case err == nil:

	case err == sql.ErrNoRows:
		existingID = 0
	default:
		return nil, false, fmt.Errorf("rem: dedup seek: %w", err)
	}

	if existingID > 0 {
		row, err := remdom.NewMemoryDomain().GetMemory(bound, existingID).Row()
		if err != nil {
			return nil, false, fmt.Errorf("rem: existing row: %w", err)
		}
		if row == nil {
			return nil, false, fmt.Errorf("rem: natural key resolved to an absent row")
		}
		if len(sh.supersedes) > 0 {
			if err := applySupersedes(bound, row.Id, sh.supersedes); err != nil {
				return nil, false, err
			}
		}
		touched := false
		if sh.importanceSet {
			row.Importance = sh.importance
			touched = true
		}
		if sh.sourceSet && sh.source != "" && (row.Source == nil || *row.Source != sh.source) {
			s := sh.source
			row.Source = &s
			touched = true
		}
		if touched {
			updated, err := remdom.NewMemoryDomain().UpdateMemory(bound, *row)
			if err != nil {
				return nil, false, fmt.Errorf("rem: existing row: %w", err)
			}
			return updated, true, nil
		}
		return row, true, nil
	}

	id, err := mintID(bound)
	if err != nil {
		return nil, false, err
	}
	kind := sh.kind
	if kind == "" {
		kind = "fact"
	}
	now := nowISO()
	var source *string
	if sh.source != "" {
		s := sh.source
		source = &s
	}
	row := remdom.Memory{
		Id:                 id,
		Scope:              scopeKey,
		ScopeLabel:         scopeLabel,
		Kind:               kind,
		Content:            sh.content,
		Source:             source,
		Importance:         sh.importance,
		Strength:           clamp01(sh.importance),
		CreatedAt:          now,
		LastConsolidatedAt: now,
		ContentMd5:         digest,
	}
	fresh, err := remdom.NewMemoryDomain().InsertMemory(bound, row)
	if err != nil {
		return nil, false, fmt.Errorf("rem: insert: %w", err)
	}
	if err := insertGrams(bound, id, gramsOf(sh.content)); err != nil {
		return nil, false, err
	}
	if ftsEnabled() {

		if _, err := tx.ExecContext(bound, `INSERT INTO memory_fts (rowid, content) VALUES ($1, $2)`, id, sh.content); err != nil {
			return nil, false, fmt.Errorf("rem: fts insert: %w", err)
		}
	}
	if len(sh.supersedes) > 0 {
		if err := applySupersedes(bound, id, sh.supersedes); err != nil {
			return nil, false, err
		}
	}
	return fresh, false, nil
}

func insertGrams(bound context.Context, id int64, grams []string) error {
	for _, gram := range grams {
		if _, err := remdom.NewTrigramDomain().InsertTrigram(bound, remdom.Trigram{MemoryId: id, Gram: gram}); err != nil {
			return fmt.Errorf("rem: grams: %w", err)
		}
	}
	return nil
}

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

func indent(text string) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n")
}

func renderMemoryLine(h Hit) string {
	head := fmt.Sprintf("m%d [%.2f] %s · %s", h.ID, h.EffectiveStrength, h.ScopeLabel, h.Kind)
	if h.SupersededBy != nil {
		head += fmt.Sprintf(" · superseded by m%d", *h.SupersededBy)
	}
	return head + "\n" + indent(h.Content)
}

func renderHits(hits []Hit, where string) string {
	if len(hits) == 0 {
		return "(no memories " + where + ")"
	}
	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(renderMemoryLine(h))
	}
	return b.String()
}

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

func Migration(cwd string) func(*sql.Tx, int, int) (string, error) {
	return func(db *sql.Tx, from, to int) (string, error) {
		removed := 0
		if from == 1 && to > from {
			n, err := removeCompactionRows(db)
			if err != nil {
				return "", err
			}
			removed = n
		}
		rescoped := 0
		oldScope := scope.ShortHash(cwd)
		newScope := scopeKey(cwd)
		if newScope != oldScope {
			var marker string
			err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, "migrated:"+oldScope).Scan(&marker)
			if err == sql.ErrNoRows {
				n, err := rescopeRows(db, oldScope, newScope, filepath.Base(cwd))
				if err != nil {
					return "", err
				}
				rescoped = n
				if _, err := db.Exec(`INSERT OR IGNORE INTO meta (key, value) VALUES (?, ?)`, "migrated:"+oldScope, newScope); err != nil {
					return "", fmt.Errorf("rem: migration: %w", err)
				}
			} else if err != nil {
				return "", fmt.Errorf("rem: migration: %w", err)
			}
		}
		if removed > 0 || rescoped > 0 {
			return fmt.Sprintf("rem migration: removed %d compaction reflections, re-scoped %d memories", removed, rescoped), nil
		}
		return "", nil
	}
}

func removeCompactionRows(db *sql.Tx) (int, error) {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM memories WHERE source = 'session compaction'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("rem: migration: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM trigrams WHERE memory_id IN (SELECT id FROM memories WHERE source = 'session compaction')`); err != nil {
		return 0, fmt.Errorf("rem: migration: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM memory_fts WHERE rowid IN (SELECT id FROM memories WHERE source = 'session compaction')`); err != nil {
		return 0, fmt.Errorf("rem: migration: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM memories WHERE source = 'session compaction'`); err != nil {
		return 0, fmt.Errorf("rem: migration: %w", err)
	}
	return n, nil
}

func rescopeRows(db *sql.Tx, oldScope, newScope, label string) (int, error) {
	res, err := db.Exec(`UPDATE memories SET scope = ?, scope_label = ? WHERE scope = ?`, newScope, label, oldScope)
	if err != nil {
		return 0, fmt.Errorf("rem: migration: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rem: migration: %w", err)
	}
	return int(n), nil
}
