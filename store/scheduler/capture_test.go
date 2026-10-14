package scheduler

import (
	"bytes"
	"context"
	"os"
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

func TestCaptureAcceptsEveryByteOfAWriteThatCrossesTheHead(t *testing.T) {
	c := newCapture(64)
	for _, n := range []int{20, 40, 40, 200} {
		in := bytes.Repeat([]byte("y"), n)
		got, err := c.Write(in)
		if err != nil || got != n {
			t.Fatalf("Write(%d bytes) = %d, %v; an io.Writer that keeps less still accepts all of it", n, got, err)
		}
	}
}

func TestAWaitDelayAfterASuccessfulExitIsNotAFault(t *testing.T) {
	dir := t.TempDir()
	res, err := RealSpawn(context.Background(), []string{"sh", "-c", "printf done; sleep 5 &"}, dir, os.Environ(), nil)
	if err != nil {
		t.Fatalf("a worker that exited cleanly must not fault on the pipes its grandchild still holds, got %v", err)
	}
	if res.Exit != 0 {
		t.Fatalf("exit = %d, want 0", res.Exit)
	}
	if res.Stdout != "done" {
		t.Fatalf("stdout = %q, want the captured output", res.Stdout)
	}
}

func TestAWaitDelayAfterAFailedExitKeepsTheExitCode(t *testing.T) {
	dir := t.TempDir()
	res, err := RealSpawn(context.Background(), []string{"sh", "-c", "printf bye; sleep 5 & exit 3"}, dir, os.Environ(), nil)
	if err != nil {
		t.Fatalf("got %v", err)
	}
	if res.Exit != 3 {
		t.Fatalf("exit = %d, want 3", res.Exit)
	}
}
