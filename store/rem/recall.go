package rem

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

const (
	decayRate = 0.02

	reinforceRate = 0.05

	rankStrengthFloor = 0.4
	rankStrengthGain  = 0.6

	reciprocalRankK = 60

	fuzzyMinOverlap     = 3
	fuzzyMinContainment = 0.5

	armCapFactor = 2

	recallKMax = 50

	importanceDefault    = 0.5
	reflectionImportance = 0.3

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

func decay(strength, days float64) float64 {
	if days <= 0 {
		return strength
	}
	return strength * math.Exp(-decayRate*days)
}

func reinforce(strength float64, accessCount int64, importance float64) float64 {
	return strength + float64(accessCount)*reinforceRate*importance
}

func consolidate(strength, days float64, accessCount int64, importance float64) float64 {
	return clamp01(reinforce(decay(strength, days), accessCount, importance))
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

func gramsOfWord(word string) []string {
	padded := fmt.Sprintf("  %s  ", word)
	var out []string
	for i := 0; i+2 < len(padded); i++ {
		out = append(out, padded[i:i+3])
	}
	return out
}

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

type armHit struct {
	memoryID int64
	arm      string
	rank     int
}

type fusedHit struct {
	score float64
	match string
}

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
