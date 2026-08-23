package rem

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	remdom "github.com/mrsirg97-rgb/rig/store/rem/domain"
	"github.com/mrsirg97-rgb/rig/store/sqlx"
)

type Hit struct {
	ID                 int64
	Scope              string
	ScopeLabel         string
	Kind               string
	Content            string
	Source             *string
	Importance         float64
	Strength           float64
	AccessCount        int64
	SupersededBy       *int64
	CreatedAt          string
	LastAccessedAt     *string
	LastConsolidatedAt string
	ContentMd5         string
	EffectiveStrength  float64
	Match              string
}

type RecallInput struct {
	Query             string
	Scope             string
	Kind              string
	K                 int
	IncludeSuperseded bool
}

func Recall(ctx context.Context, db store.DB, cwd string, in RecallInput) (string, []Hit, error) {
	k := in.K
	if k <= 0 {
		k = 10
	}
	if k > recallKMax {
		k = recallKMax
	}
	var hits []Hit
	switch {
	case in.Query == "" || strings.TrimSpace(in.Query) == "":
		h, err := transact(ctx, db, func(bound context.Context, _ *sql.Tx) ([]Hit, error) {
			return browse(bound, cwd, in, k)
		})
		if err != nil {
			return "", nil, err
		}
		hits = h
	default:
		if in.Scope == "global" || in.Scope == "all" {
			scopes, err := readScopes(in.Scope, cwd)
			if err != nil {
				return "", nil, err
			}
			h, err := transact(ctx, db, func(bound context.Context, _ *sql.Tx) ([]Hit, error) {
				return recallScoped(bound, scopes, in, k)
			})
			if err != nil {
				return "", nil, err
			}
			hits = h
		} else {

			scopes, err := readScopes("", cwd)
			if err != nil {
				return "", nil, err
			}
			h, err := transact(ctx, db, func(bound context.Context, _ *sql.Tx) ([]Hit, error) {
				return recallScoped(bound, scopes, in, k)
			})
			if err != nil {
				return "", nil, err
			}
			hits = h
			if len(hits) < k {
				fill, err := transact(ctx, db, func(bound context.Context, _ *sql.Tx) ([]Hit, error) {
					return recallScoped(bound, []string{"global"}, in, k-len(hits))
				})
				if err != nil {
					return "", nil, err
				}
				hits = append(hits, fill...)
				hits = hits[:minInt(k, len(hits))]
			}
		}
	}
	reply := fmt.Sprintf("recall: %d memories\n%s", len(hits), renderHits(hits, recallWhere(cwd, in)))
	return reply, hits, nil
}

func recallWhere(cwd string, in RecallInput) string {
	label := filepath.Base(cwd)
	var scope string
	switch in.Scope {
	case "global":
		scope = "in global"
	case "all":
		scope = "in " + label + " or global"
	default:
		scope = "in " + label + ", nor global"
	}
	if q := strings.TrimSpace(in.Query); q != "" {
		return scope + " for '" + q + "'"
	}
	return scope
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func recallScoped(bound context.Context, scopes []string, in RecallInput, k int) ([]Hit, error) {
	now := time.Now().UTC()
	cap := k * armCapFactor
	var arms [][]armHit
	if ftsEnabled() {
		if tokens := tokenize(in.Query); len(tokens) > 0 {
			hits, err := semanticArm(bound, tokens, scopes, in.Kind, cap)
			if err != nil {
				return nil, err
			}
			arms = append(arms, hits)
		}
	}
	if grams := gramsOf(in.Query); len(grams) > 0 {
		hits, err := fuzzyArm(bound, grams, scopes, in.Kind, cap)
		if err != nil {
			return nil, err
		}
		arms = append(arms, hits)
	}
	fused := fuse(arms)
	if len(fused) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(fused))
	for id := range fused {
		ids = append(ids, id)
	}
	rows, err := remdom.NewMemoryDomain().GetMemoryBatch(bound, ids).Rows()
	if err != nil {
		return nil, fmt.Errorf("rem: recall: %w", err)
	}
	rowByID := map[int64]remdom.Memory{}
	var scored []Hit
	for _, r := range rows {
		f, ok := fused[r.Id]
		if !ok {
			continue
		}
		eff := consolidate(r.Strength, daysSince(r.LastConsolidatedAt, now), r.AccessCount, r.Importance)
		scored = append(scored, hitOf(r, eff, f.match))
		rowByID[r.Id] = r
	}
	if len(scored) == 0 {
		return nil, nil
	}
	blend := func(h Hit) float64 {
		return fused[h.ID].score * (rankStrengthFloor + rankStrengthGain*h.EffectiveStrength)
	}
	var live, superseded []Hit
	for _, h := range scored {
		if h.SupersededBy == nil {
			live = append(live, h)
		} else {
			superseded = append(superseded, h)
		}
	}
	sort.SliceStable(live, func(i, j int) bool { return blend(live[i]) > blend(live[j]) })
	sort.SliceStable(superseded, func(i, j int) bool { return blend(superseded[i]) > blend(superseded[j]) })
	top := live[:minInt(k, len(live))]
	if in.IncludeSuperseded && len(top) < k {
		take := minInt(k-len(top), len(superseded))
		top = append(top, superseded[:take]...)
	}
	if len(top) > 0 {

		nowStr := now.Format(time.RFC3339)
		for _, h := range top {
			row := rowByID[h.ID]
			row.AccessCount++
			s := nowStr
			row.LastAccessedAt = &s
			if _, err := remdom.NewMemoryDomain().UpdateMemory(bound, row); err != nil {
				return nil, fmt.Errorf("rem: recall: %w", err)
			}
		}
	}
	return top, nil
}

func hitOf(r remdom.Memory, eff float64, match string) Hit {
	return Hit{
		ID:                 r.Id,
		Scope:              r.Scope,
		ScopeLabel:         r.ScopeLabel,
		Kind:               r.Kind,
		Content:            r.Content,
		Source:             r.Source,
		Importance:         r.Importance,
		Strength:           r.Strength,
		AccessCount:        r.AccessCount,
		SupersededBy:       r.SupersededBy,
		CreatedAt:          r.CreatedAt,
		LastAccessedAt:     r.LastAccessedAt,
		LastConsolidatedAt: r.LastConsolidatedAt,
		ContentMd5:         r.ContentMd5,
		EffectiveStrength:  eff,
		Match:              match,
	}
}

func semanticArm(bound context.Context, tokens []string, scopes []string, kind string, cap int) ([]armHit, error) {
	tx, err := sqlx.TxFrom(bound)
	if err != nil {
		return nil, err
	}
	var kindArg any
	if kind != "" {
		kindArg = kind
	}
	p := 1
	matchPh := fmt.Sprintf("$%d", p)
	p++
	scopePh := ""
	for _, _ = range scopes {
		if scopePh != "" {
			scopePh += ", "
		}
		scopePh += fmt.Sprintf("$%d", p)
		p++
	}
	kind1, kind2 := fmt.Sprintf("$%d", p), fmt.Sprintf("$%d", p+1)
	p += 2
	limitPh := fmt.Sprintf("$%d", p)
	sql := fmt.Sprintf(`SELECT mf.rowid AS memory_id FROM memory_fts mf
		JOIN memories m ON m.id = mf.rowid
		WHERE memory_fts MATCH %s
		  AND m.scope IN (%s)
		  AND (%s IS NULL OR m.kind = %s)
		ORDER BY rank ASC LIMIT %s`, matchPh, scopePh, kind1, kind2, limitPh)
	args := make([]any, 0, len(scopes)+4)
	args = append(args, ftsQuery(tokens))
	for _, s := range scopes {
		args = append(args, s)
	}
	args = append(args, kindArg, kindArg, cap)
	rows, err := tx.QueryContext(bound, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("rem: semantic arm: %w", err)
	}
	defer rows.Close()
	var out []armHit
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("rem: semantic arm: %w", err)
		}
		out = append(out, armHit{memoryID: id, arm: "fts", rank: len(out) + 1})
	}
	return out, rows.Err()
}

func fuzzyArm(bound context.Context, grams []string, scopes []string, kind string, cap int) ([]armHit, error) {
	tx, err := sqlx.TxFrom(bound)
	if err != nil {
		return nil, err
	}
	minOverlap := fuzzyMinOverlap
	if want := int(math.Ceil(fuzzyMinContainment * float64(len(grams)))); want > minOverlap {
		minOverlap = want
	}
	var kindArg any
	if kind != "" {
		kindArg = kind
	}
	p := 1
	var scopePh []string
	for _, _ = range grams {
		scopePh = append(scopePh, fmt.Sprintf("$%d", p))
		p++
	}
	for _, _ = range scopes {
		scopePh = append(scopePh, fmt.Sprintf("$%d", p))
		p++
	}
	kind1, kind2 := fmt.Sprintf("$%d", p), fmt.Sprintf("$%d", p+1)
	p += 2
	overlapPh, limitPh := fmt.Sprintf("$%d", p), fmt.Sprintf("$%d", p+1)
	sql := fmt.Sprintf(`SELECT t.memory_id FROM trigrams t
		JOIN memories m ON m.id = t.memory_id
		WHERE t.gram IN (%s)
		  AND m.scope IN (%s)
		  AND (%s IS NULL OR m.kind = %s)
		GROUP BY t.memory_id
		HAVING COUNT(*) >= %s
		ORDER BY COUNT(*) DESC LIMIT %s`,
		strings.Join(scopePh[:len(grams)], ", "), strings.Join(scopePh[len(grams):], ", "),
		kind1, kind2, overlapPh, limitPh)
	args := make([]any, 0, len(grams)+len(scopes)+4)
	for _, g := range grams {
		args = append(args, g)
	}
	for _, s := range scopes {
		args = append(args, s)
	}
	args = append(args, kindArg, kindArg, minOverlap, cap)
	rows, err := tx.QueryContext(bound, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("rem: fuzzy arm: %w", err)
	}
	defer rows.Close()
	var out []armHit
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("rem: fuzzy arm: %w", err)
		}
		out = append(out, armHit{memoryID: id, arm: "fuzzy", rank: len(out) + 1})
	}
	return out, rows.Err()
}

func browse(bound context.Context, cwd string, in RecallInput, k int) ([]Hit, error) {
	now := time.Now().UTC()
	scopes, err := readScopes(in.Scope, cwd)
	if err != nil {
		return nil, err
	}
	tx, err := sqlx.TxFrom(bound)
	if err != nil {
		return nil, err
	}
	var kindArg any
	if in.Kind != "" {
		kindArg = in.Kind
	}
	p := 1
	var scopePh []string
	for _, _ = range scopes {
		scopePh = append(scopePh, fmt.Sprintf("$%d", p))
		p++
	}
	kind1, kind2 := fmt.Sprintf("$%d", p), fmt.Sprintf("$%d", p+1)
	sql := fmt.Sprintf(`SELECT * FROM memories
		WHERE scope IN (%s)
		  AND (%s IS NULL OR kind = %s)
		ORDER BY created_at DESC LIMIT %d`,
		strings.Join(scopePh, ", "), kind1, kind2, k*armCapFactor)
	args := make([]any, 0, len(scopes)+2)
	for _, s := range scopes {
		args = append(args, s)
	}
	args = append(args, kindArg, kindArg)
	rows, err := tx.QueryContext(bound, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("rem: browse: %w", err)
	}
	defer rows.Close()
	var hits []Hit
	for rows.Next() {
		m, err := remdom.ScanMemory(rows)
		if err != nil {
			return nil, fmt.Errorf("rem: browse: %w", err)
		}
		hits = append(hits, hitOf(m, consolidate(m.Strength, daysSince(m.LastConsolidatedAt, now), m.AccessCount, m.Importance), "browse"))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rem: browse: %w", err)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].EffectiveStrength > hits[j].EffectiveStrength })
	return hits[:minInt(k, len(hits))], nil
}
