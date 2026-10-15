package fts

import "testing"

func TestTokenizeLowercasesAndSplits(t *testing.T) {
	got := Tokenize("LLama-Swap :8090")
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
	got := GramsOf("aa aa")
	if len(got) != len(gramsOfWord("aa")) {
		t.Errorf("gramsOf deduped = %d, want %d", len(got), len(gramsOfWord("aa")))
	}
}

func TestQueryQuotesReservedOperators(t *testing.T) {
	if got, want := Query([]string{"to", "or", "not"}), `to OR "or" OR "not"`; got != want {
		t.Errorf("ftsQuery = %q, want %q", got, want)
	}
	if got, want := Query([]string{"run", "fast"}), "run OR fast"; got != want {
		t.Errorf("ftsQuery = %q, want %q", got, want)
	}
}

func TestRankWeightAndMinOverlapAreTheSharedFusionContract(t *testing.T) {
	if ReciprocalRankK != 60 {
		t.Fatalf("reciprocal rank k = %d, want the shared 60", ReciprocalRankK)
	}
	if got, want := RankWeight(1), 1.0/61.0; got != want {
		t.Errorf("RankWeight(1) = %v, want %v (ranks are 1-based)", got, want)
	}
	if got, want := RankWeight(2), 1.0/62.0; got != want {
		t.Errorf("RankWeight(2) = %v, want %v", got, want)
	}
	if !(RankWeight(1) > RankWeight(2) && RankWeight(2) > RankWeight(3)) {
		t.Error("the weight must fall as the rank rises")
	}
	for grams, want := range map[int]int{0: 3, 1: 3, 3: 3, 6: 3, 7: 4, 100: 50, 200: 100} {
		if got := MinOverlap(grams); got != want {
			t.Errorf("MinOverlap(%d) = %d, want %d", grams, got, want)
		}
	}
}
