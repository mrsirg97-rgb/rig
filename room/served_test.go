package room

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const servedRowLen = 256

func paddedServedRow(t *testing.T, at time.Time, prompt int) []byte {
	t.Helper()
	base, err := json.Marshal(Served{At: at, Priority: PriorityTurn, Prompt: prompt, Cached: 1, Completion: 1})
	if err != nil {
		t.Fatal(err)
	}
	line := append([]byte{}, base[:len(base)-1]...)
	line = append(line, `,"pad":"`...)
	pad := servedRowLen - len(line) - len("\"}\n")
	if pad < 0 {
		t.Fatalf("the served row cannot hold its pad: %d bytes", len(base))
	}
	line = append(line, bytes.Repeat([]byte{' '}, pad)...)
	line = append(line, '"', '}', '\n')
	if len(line) != servedRowLen {
		t.Fatalf("the served row must be exactly %d bytes, got %d", servedRowLen, len(line))
	}
	return line
}

func TestServedRequestsReadTheRecordTheTailStartsOn(t *testing.T) {
	home := shortTemp(t)
	at := time.Date(2026, 10, 12, 21, 0, 0, 0, time.UTC)
	first, err := json.Marshal(Served{At: at, Priority: PriorityTurn, Prompt: 1, Cached: 1, Completion: 1})
	if err != nil {
		t.Fatal(err)
	}
	data := append([]byte{}, first...)
	data = append(data, '\n')
	rows := servedTail / servedRowLen
	for i := 0; i < rows; i++ {
		data = append(data, paddedServedRow(t, at.Add(time.Duration(i)*time.Second), 100+i)...)
	}
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(servedPath(home, "m"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	got := Recent(home, "m", rows+1)
	if len(got) != rows {
		t.Fatalf("the record the tail's cut starts on must read back: %d of %d rows", len(got), rows)
	}
	if got[0].Prompt != 100 || got[len(got)-1].Prompt != 100+rows-1 {
		t.Fatalf("the window must hold the tail's rows oldest last: %+v", got[0])
	}
}

func TestServedRequestsReadBackNewestLast(t *testing.T) {
	home := shortTemp(t)
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
	home := shortTemp(t)
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

func TestRecentAsksForNoRecordsAndGetsNone(t *testing.T) {
	home := shortTemp(t)
	for _, n := range []int{0, -5} {
		if got := Recent(home, "model", n); got != nil {
			t.Fatalf("Recent with n=%d returns %v, want none (a negative ask must not panic)", n, got)
		}
	}
}
