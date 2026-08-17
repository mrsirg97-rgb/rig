package rem

import (
	"math"
	"testing"
)

// Effective strength at recall, checkpointed decay at prune.
// The pure arithmetic is the load-bearing math — pinned by name, no I/O.

func TestDecayIsIdentityAtZeroElapsed(t *testing.T) {
	if got := decay(0.8, 0); got != 0.8 {
		t.Errorf("decay(0.8, 0) = %v, want identity", got)
	}
}

func TestDecayHalvesOverRoughlyThirtyFiveDays(t *testing.T) {
	got := decay(1.0, 35)
	if want := math.Exp(-0.7); math.Abs(got-want) > 1e-9 {
		t.Errorf("decay(1.0, 35) = %v, want %v", got, want)
	}
	if got >= 0.5 {
		t.Errorf("decay(1.0, 35) = %v, want under a half", got)
	}
}

func TestReinforceScalesByImportance(t *testing.T) {
	if got, want := reinforce(0.5, 10, 0.8), 0.9; math.Abs(got-want) > 1e-9 {
		t.Errorf("reinforce(0.5, 10, 0.8) = %v, want %v", got, want)
	}
	if got := reinforce(0.5, 10, 0.0); got != 0.5 {
		t.Errorf("reinforce(0.5, 10, 0.0) = %v, want identity", got)
	}
}

func TestConsolidateClampsToUnitInterval(t *testing.T) {
	if got := consolidate(0.99, 0, 10, 1.0); got != 1.0 {
		t.Errorf("consolidate(0.99, 0, 10, 1.0) = %v, want the clamped ceiling", got)
	}
	if got := consolidate(0.25, 100, 0, 1.0); got > 0.25 {
		t.Errorf("consolidate over an empty window = %v, want decay only", got)
	}
}

// a replay with no elapsed time and no new accesses is a no-op, so
// the pass is idempotent by construction.
func TestConsolidateReplayWithNoElapsedIsNoOp(t *testing.T) {
	once := consolidate(0.4, 0, 0, 0.7)
	twice := consolidate(once, 0, 0, 0.7)
	if math.Abs(once-twice) > 1e-9 {
		t.Errorf("replay %v -> %v, want a no-op", once, twice)
	}
}

// a decayed memory loses to a fresh one, decay
// observable without consolidate.
func TestEffectiveAgedLosesToFresh(t *testing.T) {
	aged := consolidate(0.5, 40, 0, 0.5)
	fresh := consolidate(0.5, 0, 0, 0.5)
	if !(fresh > aged) {
		t.Errorf("fresh %v !> aged %v", fresh, aged)
	}
	if !(aged < 0.3) {
		t.Errorf("aged %v, want decay observable (< 0.3)", aged)
	}
}

func TestTokenizeLowercasesAndSplits(t *testing.T) {
	got := tokenize("LLama-Swap :8090")
	want := []string{"llama", "swap", "8090"}
	if len(got) != len(want) {
		t.Fatalf("tokenize = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tokenize = %v, want %v", got, want)
		}
	}
}

// the pg_trgm convention: two-space padding, per-word grams.
func TestGramsOfWordArePaddedTrigrams(t *testing.T) {
	got := gramsOfWord("abc")
	want := []string{"  a", " ab", "abc", "bc ", "c  "}
	if len(got) != len(want) {
		t.Fatalf("gramsOfWord(abc) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("gramsOfWord(abc) = %v, want %v", got, want)
		}
	}
}

func TestGramsOfDeduplicatesPerMemory(t *testing.T) {
	got := gramsOf("aa aa")
	if len(got) != len(gramsOfWord("aa")) {
		t.Errorf("gramsOf deduped = %d, want %d", len(got), len(gramsOfWord("aa")))
	}
}

func TestFtsQueryQuotesReservedOperators(t *testing.T) {
	if got, want := ftsQuery([]string{"to", "or", "not"}), `to AND "or" AND "not"`; got != want {
		t.Errorf("ftsQuery = %q, want %q", got, want)
	}
	if got, want := ftsQuery([]string{"run", "fast"}), "run AND fast"; got != want {
		t.Errorf("ftsQuery = %q, want %q", got, want)
	}
}

// lift's reciprocal rank fusion: contribution 1/(k+rank), deduped by id,
// match annotated with the reaching arm.
func TestFuseIsReciprocalRank(t *testing.T) {
	f := fuse([][]armHit{
		{{memoryID: 1, arm: "fts", rank: 1}, {memoryID: 2, arm: "fts", rank: 1}},
		{{memoryID: 1, arm: "fuzzy", rank: 2}},
	})
	if want := 1.0/(60+1) + 1.0/(60+2); math.Abs(f[1].score-want) > 1e-12 {
		t.Errorf("fused score(m1) = %v, want %v", f[1].score, want)
	}
	if f[1].match != "both" {
		t.Errorf("fused match(m1) = %q, want both", f[1].match)
	}
	if want := 1.0 / (60 + 1); math.Abs(f[2].score-want) > 1e-12 || f[2].match != "fts" {
		t.Errorf("fused(m2) = %+v, want score %v match fts", f[2], want)
	}
}
