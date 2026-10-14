package fts

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

const (
	ReciprocalRankK = 60

	FuzzyMinOverlap = 3

	FuzzyMinContainment = 0.5
)

func RankWeight(rank int) float64 {
	return 1.0 / (float64(ReciprocalRankK) + float64(rank))
}

func MinOverlap(grams int) int {
	if want := int(math.Ceil(FuzzyMinContainment * float64(grams))); want > FuzzyMinOverlap {
		return want
	}
	return FuzzyMinOverlap
}

var wordSplit = regexp.MustCompile(`[^a-z0-9]+`)

var reserved = regexp.MustCompile(`^(and|or|not)$`)

func Tokenize(text string) []string {
	var out []string
	for _, tok := range wordSplit.Split(strings.ToLower(text), -1) {
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

func gramsOfWord(word string) []string {
	padded := "  " + word + "  "
	var out []string
	for i := 0; i+2 < len(padded); i++ {
		out = append(out, padded[i:i+3])
	}
	return out
}

func GramsOf(text string) []string {
	set := map[string]bool{}
	var out []string
	for _, word := range Tokenize(text) {
		for _, gram := range gramsOfWord(word) {
			if !set[gram] {
				set[gram] = true
				out = append(out, gram)
			}
		}
	}
	return out
}

func Query(tokens []string) string {
	parts := make([]string, len(tokens))
	for i, tok := range tokens {
		if reserved.MatchString(tok) {
			parts[i] = fmt.Sprintf("%q", tok)
		} else {
			parts[i] = tok
		}
	}
	return strings.Join(parts, " OR ")
}
