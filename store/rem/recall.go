// recall's pure core: the consolidation arithmetic, the
// lexical shapes of the two arms, and reciprocal rank fusion (lift's RRF,
// k = 60). Zero I/O — the load-bearing math, testable by name.
package rem

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Constants, named one comment each.
const (
	// decayRate: exponential decay per day; strength halves over
	// roughly 35 days of neglect (exp(-0.02*35) ~ 0.5).
	decayRate = 0.02
	// reinforceRate: reinforcement per access, scaled by importance —
	// one access to an importance-1 memory adds 5% strength.
	reinforceRate = 0.05
	// rankStrengthFloor / rankStrengthGain: the blend over the fused
	// rank, floor + gain * effective_strength.
	rankStrengthFloor = 0.4
	rankStrengthGain  = 0.6
	// reciprocalRankK: the RRF constant (lift's).
	reciprocalRankK = 60
	// fuzzyMinOverlap / fuzzyMinContainment: the trigram arm's
	// minimum absolute overlap and containment floor.
	fuzzyMinOverlap     = 3
	fuzzyMinContainment = 0.5
	// armCapFactor: each arm fetches at most 2k to allow fusion.
	armCapFactor = 2
	// recallKMax: the live-hit budget ceiling.
	recallKMax = 50
	// importanceDefault / reflectionImportance: the write-side defaults.
	importanceDefault    = 0.5
	reflectionImportance = 0.3
	// autoReflectionImportance: the compaction-entry reflection.
	autoReflectionImportance = 0.2
	// kindReflection: the distilled-memory kind.
	kindReflection = "reflection"
)

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// decay applies the exponential term for days elapsed. Zero elapsed is
// the identity; the tail is asymptotic and never reaches zero.
func decay(strength, days float64) float64 {
	if days <= 0 {
		return strength
	}
	return strength * math.Exp(-decayRate*days)
}

// reinforce adds the access reinforcement, scaled by importance.
func reinforce(strength float64, accessCount int64, importance float64) float64 {
	return strength + float64(accessCount)*reinforceRate*importance
}

// consolidate is the whole formula at a checkpoint: decay by days since
// the last checkpoint, reinforce the accesses since then, clamp. Because
// recall's effective computation uses exactly these inputs, the two
// paths agree: effective-at-recall equals what consolidate would
// persist, and consolidating later cannot double-count.
func consolidate(strength, days float64, accessCount int64, importance float64) float64 {
	return clamp01(reinforce(decay(strength, days), accessCount, importance))
}

// --- the arms' lexical shapes ---

var wordSplit = regexp.MustCompile(`[^a-z0-9]+`)

// tokenize: lowercase, split on non-alphanumeric — the word unit both
// arms rank by.
func tokenize(text string) []string {
	var out []string
	for _, tok := range wordSplit.Split(strings.ToLower(text), -1) {
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

// gramsOfWord: per-word padded trigrams, the pg_trgm convention
// (two-space padding) — the sqlite analogue of word_similarity.
func gramsOfWord(word string) []string {
	padded := fmt.Sprintf("  %s  ", word)
	var out []string
	for i := 0; i+2 < len(padded); i++ {
		out = append(out, padded[i:i+3])
	}
	return out
}

// gramsOf: the deduplicated gram set of a text — bounded by content.
func gramsOf(text string) []string {
	set := map[string]bool{}
	var out []string
	for _, word := range tokenize(text) {
		for _, gram := range gramsOfWord(word) {
			if !set[gram] {
				set[gram] = true
				out = append(out, gram)
			}
		}
	}
	return out
}

var reservedFTS = regexp.MustCompile(`^(and|or|not)$`)

// ftsQuery: the query grammar — reserved FTS operators quoted, terms
// AND-ed.
func ftsQuery(tokens []string) string {
	parts := make([]string, len(tokens))
	for i, tok := range tokens {
		if reservedFTS.MatchString(tok) {
			parts[i] = fmt.Sprintf("%q", tok)
		} else {
			parts[i] = tok
		}
	}
	return strings.Join(parts, " AND ")
}

// --- fusion ---

type armHit struct {
	memoryID int64
	arm      string // fts | fuzzy
	rank     int
}

type fusedHit struct {
	score float64
	match string // fts | fuzzy | both
}

// fuse: reciprocal rank over the arms' rankings — contribution
// 1/(k+rank), deduped by memory id, annotated with the reaching arm.
func fuse(arms [][]armHit) map[int64]fusedHit {
	out := map[int64]fusedHit{}
	for _, arm := range arms {
		for _, hit := range arm {
			prev, ok := out[hit.memoryID]
			contribution := 1.0 / (float64(reciprocalRankK) + float64(hit.rank))
			if ok {
				out[hit.memoryID] = fusedHit{score: prev.score + contribution, match: "both"}
			} else {
				out[hit.memoryID] = fusedHit{score: contribution, match: hit.arm}
			}
		}
	}
	return out
}
