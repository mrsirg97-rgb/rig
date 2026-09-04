package scheduler

import (
	"bytes"
	"strings"
	"testing"
)

func TestCapturePassesSmallOutputVerbatim(t *testing.T) {
	c := newCapture(64)
	data := []byte("small output")
	if _, err := c.Write(data); err != nil {
		t.Fatal(err)
	}
	if c.String() != string(data) {
		t.Fatalf("capture = %q, want verbatim %q", c.String(), data)
	}
}

func TestCaptureBoundsTheOutputAndKeepsHeadAndTail(t *testing.T) {
	c := newCapture(64)
	in := append(bytes.Repeat([]byte("A"), 40), bytes.Repeat([]byte("B"), 40)...)
	if _, err := c.Write(in); err != nil {
		t.Fatal(err)
	}
	got := c.String()
	parts := strings.SplitN(got, "\n[spawn: output truncated", 2)
	if len(parts) != 2 {
		t.Fatalf("the truncation marker is missing: %q", got)
	}
	want := strings.Repeat("A", 32) + strings.Repeat("B", 32)
	if parts[0] != want {
		t.Fatalf("capture kept %q, want the head and tail %q", parts[0], want)
	}
}

func TestCaptureNeverGrowsBeyondTheCap(t *testing.T) {
	c := newCapture(256)
	chunk := bytes.Repeat([]byte("x"), 97)
	for i := 0; i < 1000; i++ {
		if _, err := c.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.String()) > 256+128 {
		t.Fatalf("capture grew to %d bytes, want at most the cap plus the marker", len(c.String()))
	}
}
