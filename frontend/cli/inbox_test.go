package cli_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

func TestTheCliFoldsReturnsIntoTheNextTurnInOrder(t *testing.T) {
	r := build(t)
	r.fe.Notify(core.WorkerDone{N: 1, Task: "sweep one", Content: "one\ndelegate: exit 0 · 12ms · session s1 · log runs/j1/a.log", Exit: 0, Duration: 62 * time.Second, Session: "s1", Log: "runs/j1/a.log"})
	r.fe.Notify(core.WorkerDone{N: 2, Task: "sweep two", Content: "two\ndelegate: exit 7 · 9ms · session s2 · log runs/j2/b.log", Exit: 7, Duration: 2 * time.Minute, Session: "s2", Log: "runs/j2/b.log"})

	got, err := r.fe.Input(context.Background())
	if err != nil {
		t.Fatalf("input: %v", err)
	}
	if !strings.HasPrefix(got, "delegate #1 returned · exit 0 · 1m2s · session s1\n") {
		t.Fatalf("the block opens with the first return: %q", got)
	}
	if strings.Index(got, "delegate #1") > strings.Index(got, "delegate #2") {
		t.Fatalf("arrival order: %q", got)
	}
	if !strings.Contains(got, "delegate #2 returned · exit 7 · 2m0s · session s2") {
		t.Fatalf("each head line names its exit and its session: %q", got)
	}
	if !strings.Contains(got, "one\n") || !strings.Contains(got, "two\n") {
		t.Fatalf("the contents ride with their heads: %q", got)
	}
	printed := r.out.String()
	for _, want := range []string{"delegate #1 returned · exit 0 · 1m2s · session s1", "delegate #2 returned · exit 7 · 2m0s · session s2"} {
		if !strings.Contains(printed, want) {
			t.Fatalf("the terminal shows what the model was told (%q):\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "one\ndelegate:") {
		t.Fatalf("the contents are the model's to read, not the terminal's to scroll: %q", printed)
	}
}

func TestTheCliWakesForAReturnWhileItWaits(t *testing.T) {
	r := build(t)
	done := make(chan string, 1)
	go func() {
		line, err := r.fe.Input(context.Background())
		if err != nil {
			t.Errorf("input: %v", err)
		}
		done <- line
	}()
	r.fe.Notify(core.WorkerDone{N: 3, Content: "three", Exit: 0, Duration: time.Second, Session: "s3"})
	select {
	case line := <-done:
		if !strings.HasPrefix(line, "delegate #3 returned") {
			t.Fatalf("a return while waiting is itself an input: %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Input never woke for the return")
	}
}
