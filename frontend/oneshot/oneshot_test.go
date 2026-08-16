package oneshot_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/looper/core"
	"github.com/mrsirg97-rgb/looper/frontend/oneshot"
)

func TestOneShotFeedsExactlyOnePromptThenEOFs(t *testing.T) {
	o := &oneshot.OneShot{Prompt: "do the thing"}
	p, err := o.Input(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p != "do the thing" {
		t.Fatalf("first input %q", p)
	}
	// second pull: the session ends, cleanly
	if _, err := o.Input(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("second input err %v, want io.EOF", err)
	}
}

func TestOneShotErrPromptNamesTheEmptyConstruction(t *testing.T) {
	if err := oneshot.ErrPrompt(""); !errors.Is(err, oneshot.ErrOneShot) {
		t.Fatalf("ErrPrompt(\"\") %v", err)
	}
	if err := oneshot.ErrPrompt("  \n"); !errors.Is(err, oneshot.ErrOneShot) {
		t.Fatalf("ErrPrompt(blank) %v", err)
	}
	if err := oneshot.ErrPrompt("real"); err != nil {
		t.Fatalf("ErrPrompt(real) %v", err)
	}
}

func TestOneShotNotifyRendersAssistantTextAndFaultsLoud(t *testing.T) {
	var sb strings.Builder
	o := &oneshot.OneShot{Out: &sb}
	o.Notify(core.TextDelta{Text: "hel"})
	o.Notify(core.ToolCallEvent{Call: core.ToolCall{Name: "bash"}}) // not the report
	o.Notify(core.TextDelta{Text: "lo"})
	o.Notify(core.Done{})
	want := "hello\n"
	if sb.String() != want {
		t.Fatalf("rendered %q, want %q", sb.String(), want)
	}
	var fb strings.Builder
	o = &oneshot.OneShot{Out: &fb}
	o.Notify(core.Fault{Err: errors.New("boom")})
	if !strings.Contains(fb.String(), "boom") {
		t.Fatalf("fault voice lost: %q", fb.String())
	}
	if !o.Faulted() {
		t.Fatal("fault did not mark the session")
	}
	o2 := &oneshot.OneShot{Out: &fb}
	if o2.Faulted() {
		t.Fatal("a fresh session reports faulted")
	}
}
