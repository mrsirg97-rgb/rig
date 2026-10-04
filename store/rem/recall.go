package rem

import (
	"math"
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
