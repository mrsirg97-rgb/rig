// Package rem is the memory store: pane's REM_SPEC semantics, Go over the
// generated substrate. SPEC_STATE's "### rem" section is the spec; pane's
// REM_SPEC carries the voice and the named cases.
//
// Writes land through the generated accessors inside one serializable
// transaction per operation; the store owns a small named raw surface,
// each statement commented as such: the natural-key dedup seek, the
// recall arms (FTS MATCH, the trigram overlap) and the browse ordering,
// the prune selection predicate, the supersession-clearing UPDATE (the
// runtime behaviour of REM_SPEC's ON DELETE SET NULL — the camera emits
// no foreign keys at all), and the fts rowid bookkeeping (the virtual
// table is not a container the grammar speaks). Ids are minted from a
// meta counter inside the caller's transaction: strictly increasing,
// never reused — REM_SPEC's AUTOINCREMENT rule, kept by minting.
package rem

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
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
	"github.com/mrsirg97-rgb/rig/store/sqlx"
)

// SchemaVersion is applied at Open; mismatches are refused loudly.
const SchemaVersion = 1

// DDL: the generated statements alone (the drift diff's left side).
func DDL() []string { return remdd.Statements() }

// Statements: the store's schema in application order — the generated
// DDL, then extra.sql (pane's indexes), then fts.sql (the FTS5 virtual
// table). A driver that cannot create the table fails loudly here;
// recall's shipped policy on capability absence is the fuzzy-only
// degradation (REM_SPEC's named case) — unreachable under the bundled
// pure-Go driver, which always ships FTS5.
func Statements() []string {
	out := remdd.Statements()
	out = append(out, remmeta.ExtraStatements()...)
	return append(out, remmeta.FtsStatements()...)
}

// --- fts capability: production rides the schema application (fts.sql
// created the table at open, so a live handle implies the capability);
// the seam simulates an absent build for REM_SPEC's named degradation
// cases and gates the semantic arm's statements out — prepare included.
var (
	ftsOverrideSet atomic.Bool
	ftsOverride    atomic.Bool
)

// SetFtsAvailable is the degradation seam: nil clears the override.
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

// --- scope ---

// shortHash: the scope key of a workspace — sha1(cwd)[:12], pane's.
func shortHash(cwd string) string {
	d := sha1.Sum([]byte(cwd))
	return hex.EncodeToString(d[:])[:12]
}

// writeScope: the write-side scope shaping. pane's voice verbatim.
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
	return shortHash(cwd), label, nil
}

// readScopes: the read-side scope shaping — global, project, both.
func readScopes(scope, cwd string) ([]string, error) {
	switch scope {
	case "global":
		return []string{"global"}, nil
	case "all":
		return []string{shortHash(cwd), "global"}, nil
	case "", "project":
		return []string{shortHash(cwd)}, nil
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

// --- plumbing: one transaction per operation, serializable ---

func zero[T any]() T { var z T; return z }

// transact opens the operation's single serializable transaction,
// threads the bound context through the act, commits on success, rolls
// back on any error.
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

// --- id minting: the meta counter, REM_SPEC's AUTOINCREMENT rule ---

const idCounterKey = "memory_id_seq"

// mintID mints the next memory id from the meta counter inside the
// caller's write transaction: strictly increasing, never reused — a
// pruned id must never silently re-point a supersession reference.
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

// --- supersedes ---

// applySupersedes: pane's trust-chain update through the generated
// surface. Missing targets refuse loudly — a typo must never silently
// corrupt the chain. Self-supersedes are filtered: re-superseding one's
// own id is a no-op, never a self-demotion.
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

// --- writes ---

// LearnInput is one committed fact or constraint.
type LearnInput struct {
	Content       string
	Kind          string
	Importance    float64
	ImportanceSet bool // the caller passed an explicit importance
	Scope         string
	Source        string // "" lands NULL
	Supersedes    []int64
}

// ReflectInput is one distilled memory with its raw source.
type ReflectInput struct {
	Content       string
	Importance    float64
	ImportanceSet bool
	Scope         string
	Source        string // explicit provenance; "" lands NULL
}

type writeResult struct {
	reply    string
	mem      *remdom.Memory
	existing bool
}

// Learn commits a fact idempotently on (scope, md5(content)) — the
// natural key. Existing rows accept importance and supersedes updates;
// content is untouched. Pane's reply voices verbatim.
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

// Reflect stores the distilled memory with its raw source — provenance,
// never indexed, shown only. Kind defaults to reflection; existing rows
// accept importance and source updates.
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

// AutoReflect is the compaction-entry path (pane's session_compact): the
// distilled summary as a low-importance reflection, scoped to the
// workspace, deduped by content. Blank summaries are inert. The caller
// is fire-and-forget: a store failure never crashes the session.
func AutoReflect(ctx context.Context, db store.DB, cwd, summary string) (string, error) {
	if strings.TrimSpace(summary) == "" {
		return "", nil
	}
	reply, _, _, err := Reflect(ctx, db, cwd, ReflectInput{
		Content:    summary,
		Importance: autoReflectionImportance,
		Source:     "session compaction",
	})
	return reply, err
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

// storeOrTouch: pane's write path — natural-key find-or-create, trigram
// bookkeeping, gated fts bookkeeping, supersedes. One transaction.
func storeOrTouch(bound context.Context, sh writeShape, cwd string) (*remdom.Memory, bool, error) {
	scopeKey, scopeLabel, err := writeScope(sh.scope, cwd)
	if err != nil {
		return nil, false, err
	}
	digest := md5hex(sh.content)

	// Natural-key dedup seek — pane's find-or-create predicate; no
	// generated accessor spans a non-primary predicate. Named.
	tx, err := sqlx.TxFrom(bound)
	if err != nil {
		return nil, false, err
	}
	var existingID int64
	err = tx.QueryRowContext(bound, `SELECT id FROM memories WHERE scope = $1 AND content_md5 = $2`, scopeKey, digest).Scan(&existingID)
	switch {
	case err == nil:
		// found below
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
		// Pane's gated FTS bookkeeping (REM_SPEC E): keyed by rowid,
		// suppressed entirely — prepare included — when the capability
		// is absent. Named.
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

// insertGrams: the shadow rows through the generated accessor, deduped
// per memory.
func insertGrams(bound context.Context, id int64, grams []string) error {
	for _, gram := range grams {
		if _, err := remdom.NewTrigramDomain().InsertTrigram(bound, remdom.Trigram{MemoryId: id, Gram: gram}); err != nil {
			return fmt.Errorf("rem: grams: %w", err)
		}
	}
	return nil
}

// --- prune ---

// PruneInput is one prune call.
type PruneInput struct {
	Verb          string // consolidate | remove | reduce
	IDs           []int64
	Scope         string
	Kind          string
	OlderThanDays int
	Importance    *float64 // the reduce target
}

// Prune: consolidate persists the decay pass (idempotent by
// construction); remove/reduce are bounded by a selection and report
// actual effects. Pane's voices verbatim.
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

// candidatesOf: the selection — ids win; else the criteria predicate;
// else the whole store (consolidate's named default).
func candidatesOf(bound context.Context, in PruneInput, cwd string, whole bool) ([]int64, error) {
	if len(in.IDs) > 0 {
		return dedupeIDs(in.IDs), nil
	}
	hasCriteria := in.Kind != "" || in.OlderThanDays != 0 || in.Scope != ""
	if !hasCriteria {
		if !whole {
			return nil, fmt.Errorf("rem: prune needs ids or criteria (kind/older_than_days/scope)")
		}
		// Whole-store candidate enumeration — pane's consolidate default.
		// Named: no generated accessor spans an unkeyed scan.
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
	// The prune selection predicate — pane's own statement shape; no
	// generated accessor spans it. Named.
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

// consolidatePass: pane's decay pass — effective-at-recall equals what
// this persists; a replay with no elapsed time and no new accesses is a
// no-op, so the pass is idempotent by construction.
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

// daysSince: elapsed days since an ISO timestamp; unparseable or future
// timestamps read as zero (pane's daysBetween).
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

// removeMemories: the prune's bookkeeping, pane's statement shapes, all
// inside the caller's transaction. Missing ids count zero, never phantom
// deletions.
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
		// REM_SPEC's ON DELETE SET NULL, runtime behaviour: removing the
		// superseding memory unsupersedes the older one, whose content is
		// then the best surviving record. The camera emits no foreign keys
		// at all, so the store clears the pointers itself, same
		// transaction. Named.
		if _, err := tx.ExecContext(bound, `UPDATE memories SET superseded_by = NULL WHERE superseded_by = $1`, id); err != nil {
			return removed, fmt.Errorf("rem: remove: %w", err)
		}
		// Pane's defense-in-depth trigram cleanup (REM_SPEC E). Named.
		if _, err := tx.ExecContext(bound, `DELETE FROM trigrams WHERE memory_id = $1`, id); err != nil {
			return removed, fmt.Errorf("rem: remove: %w", err)
		}
		if ftsEnabled() {
			// Pane's gated FTS bookkeeping (REM_SPEC E), keyed by rowid —
			// an fts-less build must not even compile the statement.
			// Named.
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

// reduceImportance: lowers importance over the selection; reports
// actual effects. Selection precedes the importance check — pane's
// execute-time order, so the selection voice wins on bare calls.
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

// --- reply render: pane's shaping verbatim ---

// indent: two-space per line, pane's _render-kit convention.
func indent(text string) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n")
}

// renderMemoryLine is one hit row: the head (id, effective strength,
// scope label, kind), the supersession tag, the indented content.
func renderMemoryLine(h Hit) string {
	head := fmt.Sprintf("m%d [%.2f] %s · %s", h.ID, h.EffectiveStrength, h.ScopeLabel, h.Kind)
	if h.SupersededBy != nil {
		head += fmt.Sprintf(" · superseded by m%d", *h.SupersededBy)
	}
	return head + "\n" + indent(h.Content)
}

func renderHits(hits []Hit) string {
	if len(hits) == 0 {
		return "(no memories)"
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
