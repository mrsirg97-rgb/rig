package graph

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/store"
)

const (
	reciprocalRankK     = 60
	fuzzyMinOverlap     = 3
	fuzzyMinContainment = 0.5
)

type Scorer interface {
	Score(ctx context.Context, task string, items []string) ([]decision.PackVerdict, error)
}

func (q *Queue) packTask(ctx context.Context, db store.DB, root, task string) (string, error) {
	cands, err := lexicalCandidates(ctx, db, task, q.itemCap)
	if err != nil {
		return "", err
	}
	if len(cands) == 0 {
		return "", fmt.Errorf("graph: no symbol in the map matches %q; run index first", task)
	}
	type candidate struct {
		sym  symRow
		item string
		prob float64
	}
	var items []candidate
	total := 0
	for _, c := range cands {
		item, err := candidateItem(root, c)
		if err != nil {
			q.say("graph: %v", err)
			continue
		}
		if len(items) > 0 && total+len(item) >= q.itemCap {
			break
		}
		total += len(item)
		items = append(items, candidate{sym: c, item: item})
	}
	if len(items) == 0 {
		return "", fmt.Errorf("graph: no symbol in the map matches %q; run index first", task)
	}
	var yes []candidate
	var unsure []string
	wobbly := map[string]bool{}
	scored := 0
	if q.scorer == nil {
		yes = items
	} else {
		texts := make([]string, len(items))
		for i, c := range items {
			texts[i] = c.item
		}
		verdicts, err := q.scorer.Score(ctx, task, texts)
		if err != nil {
			return "", err
		}
		for i, v := range verdicts {
			if v.Yes {
				items[i].prob = v.Probability
				yes = append(yes, items[i])
			}
			if v.Answered {
				scored++
			}
			if v.Unsure {
				wobbly[items[i].sym.Package+"\x00"+items[i].sym.Name] = true
				unsure = append(unsure, baseName(items[i].sym.Package)+"."+items[i].sym.Name)
			}
		}
		sort.SliceStable(yes, func(i, j int) bool { return yes[i].prob > yes[j].prob })
	}
	var b strings.Builder
	loaded := 0
	spent := false
	done := map[string]bool{}
	for _, c := range yes {
		block, err := q.packOne(ctx, db, root, c.sym)
		if err != nil {
			return "", err
		}
		if loaded > 0 && loaded+len(block) > q.loadCap {
			spent = true
			break
		}
		b.WriteString(block)
		loaded += len(block)
		done[c.sym.Package+"\x00"+c.sym.Name] = true
	}
	if q.scorer != nil && !spent {
		for _, c := range items {
			key := c.sym.Package + "\x00" + c.sym.Name
			if done[key] || wobbly[key] {
				continue
			}
			block, err := q.packOne(ctx, db, root, c.sym)
			if err != nil {
				return "", err
			}
			if loaded > 0 && loaded+len(block) > q.loadCap {
				break
			}
			b.WriteString(block)
			loaded += len(block)
			done[key] = true
		}
	}
	if len(unsure) > 0 {
		fmt.Fprintf(&b, "unsure (%d) — the server did not say yes; pack one by hand\n", len(unsure))
		for _, n := range unsure {
			fmt.Fprintf(&b, "  %s\n", n)
		}
	}
	coverage(ctx, db, root, &b)
	if q.scorer != nil {
		fmt.Fprintf(&b, "scored %d candidates\n", scored)
	}
	return b.String(), nil
}

func candidateItem(root string, s symRow) (string, error) {
	lines, err := sigLines(root, s.File, s.Line, s.EndLine)
	if err != nil {
		return "", fmt.Errorf("graph: %s: %w", s.File, err)
	}
	texts := make([]string, 0, len(lines))
	for _, l := range lines {
		texts = append(texts, l.text)
	}
	return fmt.Sprintf("%s — %s:%d", strings.Join(texts, " "), s.File, s.Line), nil
}

func lexicalCandidates(ctx context.Context, db store.DB, task string, limit int) ([]symRow, error) {
	arms := make([][]armHit, 0, 2)
	if tokens := tokenize(task); len(tokens) > 0 {
		hits, err := ftsArm(ctx, db, ftsQuery(tokens), limit)
		if err != nil {
			return nil, err
		}
		arms = append(arms, hits)
	}
	if grams := gramsOf(task); len(grams) > 0 {
		hits, err := gramArm(ctx, db, grams, limit)
		if err != nil {
			return nil, err
		}
		arms = append(arms, hits)
	}
	return fuseArms(arms), nil
}

type armHit struct {
	row  symRow
	rank int
}

func ftsArm(ctx context.Context, db store.DB, match string, limit int) ([]armHit, error) {
	rows, err := db.QueryContext(ctx, `SELECT package, name, kind, file, line, end_line FROM symbol_fts WHERE symbol_fts MATCH ? ORDER BY rank LIMIT ?`, match, limit)
	if err != nil {
		return nil, fmt.Errorf("graph: fts arm: %w", err)
	}
	defer rows.Close()
	var out []armHit
	for rows.Next() {
		var s symRow
		if err := rows.Scan(&s.Package, &s.Name, &s.Kind, &s.File, &s.Line, &s.EndLine); err != nil {
			return nil, fmt.Errorf("graph: fts arm: %w", err)
		}
		out = append(out, armHit{row: s, rank: len(out) + 1})
	}
	return out, rows.Err()
}

func gramArm(ctx context.Context, db store.DB, grams []string, limit int) ([]armHit, error) {
	minOverlap := fuzzyMinOverlap
	if want := int(math.Ceil(fuzzyMinContainment * float64(len(grams)))); want > minOverlap {
		minOverlap = want
	}
	places := make([]string, len(grams))
	args := make([]any, 0, len(grams)+2)
	for i, g := range grams {
		places[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, g)
	}
	overlapPh, limitPh := fmt.Sprintf("$%d", len(grams)+1), fmt.Sprintf("$%d", len(grams)+2)
	query := fmt.Sprintf(`SELECT package, name, kind, file, line, end_line FROM symbol_grams
		WHERE gram IN (%s)
		GROUP BY package, name, kind, file, line, end_line
		HAVING COUNT(*) >= %s
		ORDER BY COUNT(*) DESC LIMIT %s`, strings.Join(places, ", "), overlapPh, limitPh)
	args = append(args, minOverlap, limit)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("graph: fuzzy arm: %w", err)
	}
	defer rows.Close()
	var out []armHit
	for rows.Next() {
		var s symRow
		if err := rows.Scan(&s.Package, &s.Name, &s.Kind, &s.File, &s.Line, &s.EndLine); err != nil {
			return nil, fmt.Errorf("graph: fuzzy arm: %w", err)
		}
		out = append(out, armHit{row: s, rank: len(out) + 1})
	}
	return out, rows.Err()
}

func fuseArms(arms [][]armHit) []symRow {
	scores := map[string]float64{}
	rows := map[string]symRow{}
	for _, arm := range arms {
		for _, hit := range arm {
			key := hit.row.Package + "\x00" + hit.row.Name
			scores[key] += 1.0 / (float64(reciprocalRankK) + float64(hit.rank))
			rows[key] = hit.row
		}
	}
	keys := make([]string, 0, len(scores))
	for k := range scores {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if scores[keys[i]] != scores[keys[j]] {
			return scores[keys[i]] > scores[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]symRow, 0, len(keys))
	for _, k := range keys {
		out = append(out, rows[k])
	}
	return out
}

var wordSplit = regexp.MustCompile(`[^a-z0-9]+`)

func tokenize(text string) []string {
	var out []string
	for _, tok := range wordSplit.Split(strings.ToLower(text), -1) {
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

func gramsOf(text string) []string {
	set := map[string]bool{}
	var out []string
	for _, word := range tokenize(text) {
		padded := fmt.Sprintf("  %s  ", word)
		for i := 0; i+2 < len(padded); i++ {
			gram := padded[i : i+3]
			if !set[gram] {
				set[gram] = true
				out = append(out, gram)
			}
		}
	}
	return out
}

var reservedFTS = regexp.MustCompile(`^(and|or|not)$`)

func ftsQuery(tokens []string) string {
	parts := make([]string, len(tokens))
	for i, tok := range tokens {
		if reservedFTS.MatchString(tok) {
			parts[i] = fmt.Sprintf("%q", tok)
		} else {
			parts[i] = tok
		}
	}
	return strings.Join(parts, " OR ")
}

func symbolGrams(s Symbol) []string {
	return gramsOf(strings.Join([]string{s.Name, s.Kind, s.Package, s.File}, " "))
}
