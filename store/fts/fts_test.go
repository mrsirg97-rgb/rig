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
