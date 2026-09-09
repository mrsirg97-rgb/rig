package bash

import (
	"bytes"
	"strings"
	"testing"
)

func TestBoundedWriterKeepsTheHeadAndNeverBlocksTheChild(t *testing.T) {
	b := newBounded(outputCap)
	full := bytes.Repeat([]byte("x"), outputCap*3)
	if n, err := b.Write(full); err != nil || n != len(full) {
		t.Fatalf("the child's write must be fully consumed, got n=%d err=%v", n, err)
	}
	got := b.String()
	want := strings.Repeat("x", outputCap) + "\n[output truncated]"
	if got != want {
		t.Fatalf("the kept output must be the head plus the marker (%d bytes), got %d bytes", len(want), len(got))
	}
}

func TestBoundedWriterKeepsTheHeadAcrossChunkedWrites(t *testing.T) {
	b := newBounded(outputCap)
	chunk := bytes.Repeat([]byte("z"), 17)
	total := 0
	for total < outputCap*2 {
		if _, err := b.Write(chunk); err != nil {
			t.Fatalf("chunked write: %v", err)
		}
		total += len(chunk)
	}
	got := b.String()
	if !strings.HasSuffix(got, "\n[output truncated]") {
		t.Fatalf("a stream past the cap must carry the marker, got %d bytes", len(got))
	}
	if len(got) != outputCap+len("\n[output truncated]") {
		t.Fatalf("the kept stream must be exactly the cap plus the marker, got %d bytes", len(got))
	}
}

func TestBoundedWriterAtTheCapCarriesNoMarker(t *testing.T) {
	b := newBounded(outputCap)
	exact := bytes.Repeat([]byte("y"), outputCap)
	if _, err := b.Write(exact); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != string(exact) {
		t.Fatalf("at the cap the output is untouched, got %d bytes", len(got))
	}
}
