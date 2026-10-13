package room

import (
	"testing"
	"time"
)

func TestServedRequestsReadBackNewestLast(t *testing.T) {
	home := t.TempDir()
	if got := Recent(home, "m", 4); len(got) != 0 {
		t.Fatalf("a model nobody served reads empty, got %d", len(got))
	}
	at := time.Date(2026, 10, 12, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		if err := Record(home, "m", Served{At: at.Add(time.Duration(i) * time.Second), Priority: PriorityTurn, Prompt: 1000 + i, Cached: 990, Completion: 10, DecodePerSecond: 90.6}); err != nil {
			t.Fatal(err)
		}
	}
	got := Recent(home, "m", 4)
	if len(got) != 4 || got[0].Prompt != 1002 || got[3].Prompt != 1005 {
		t.Fatalf("want the last four, oldest first: %+v", got)
	}
	if got[3].DecodePerSecond != 90.6 || got[3].Priority != PriorityTurn {
		t.Fatalf("the row must read back whole: %+v", got[3])
	}
}

func TestServedRequestsReadOnlyTheFilesTail(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < 1200; i++ {
		if err := Record(home, "m", Served{Prompt: i, Completion: 1}); err != nil {
			t.Fatal(err)
		}
	}
	got := Recent(home, "m", 2000)
	if len(got) == 0 || len(got) >= 1200 {
		t.Fatalf("a long file is read from its tail: %d rows", len(got))
	}
	if got[len(got)-1].Prompt != 1199 {
		t.Fatalf("the newest row must be last: %+v", got[len(got)-1])
	}
}
