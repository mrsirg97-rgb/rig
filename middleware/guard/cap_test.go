package guard_test

// The result bound (SPEC_HARDENING decision 9): every tool result is
// bounded before it reaches the transcript, in one place — an oversized
// result truncates to the head and the tail with the loud [TRUNCATED]
// marker naming the full size, and a small result passes byte-identical.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/guard"
)

// A small result is byte-identical: the wall never touches what fits.
func TestCapLeavesASmallResultByteIdentical(t *testing.T) {
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return "hello world", nil
	}
	exec = guard.Cap(64 * 1024).Wrap(exec)
	content, err := exec(context.Background(), core.ToolCall{ID: "c", Name: "read", Args: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("a small result must pass: %v", err)
	}
	if content != "hello world" {
		t.Fatalf("a small result must stay byte-identical, got %q", content)
	}
}

// An oversized result truncates to the head and the tail, the middle
// elided, with the marker naming the full size and the teaching line.
func TestCapTruncatesHeadAndTailNamingTheSize(t *testing.T) {
	full := strings.Repeat("h", 100) + strings.Repeat("m", 1000) + strings.Repeat("t", 100)
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return full, nil
	}
	exec = guard.Cap(256).Wrap(exec)
	content, err := exec(context.Background(), core.ToolCall{ID: "c", Name: "read", Args: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("the truncation must not fail the turn: %v", err)
	}
	// the head and the tail are kept, the middle is gone.
	if !strings.HasPrefix(content, "h") || !strings.HasSuffix(content, "t") {
		t.Fatalf("the head and the tail must be kept, got %q", content)
	}
	if strings.Contains(content, strings.Repeat("m", 3)) {
		t.Fatalf("the middle must be elided, got %q", content)
	}
	// the marker names the full size (1200), what was kept, and the reach.
	for _, want := range []string{"[TRUNCATED]", "1200", "head", "tail", "re-read a narrower range"} {
		if !strings.Contains(content, want) {
			t.Fatalf("the marker must name the full size and the teaching line, got %q", content)
		}
	}
}

// The cap bounds a failing tool's fed-back content too: the wall is
// behind the chain, not ahead of it, and a tiny cap still names the size.
func TestCapBoundsTheRefusalContent(t *testing.T) {
	long := strings.Repeat("e", 1000) + strings.Repeat("r", 1000)
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return long, nil
	}
	exec = guard.Cap(32).Wrap(exec)
	content, err := exec(context.Background(), core.ToolCall{ID: "c", Name: "read", Args: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("the cap must not add an error to a passing tool: %v", err)
	}
	// a 32-byte cap cannot hold any of the content beside the marker:
	// the marker is the whole reply, and it names the full size (2000).
	if strings.Contains(content, "eee") {
		t.Fatalf("a tiny cap must elide the content whole, got %q", content)
	}
	for _, want := range []string{"[TRUNCATED]", "2000", "re-read a narrower range"} {
		if !strings.Contains(content, want) {
			t.Fatalf("the marker must survive a tiny cap and name the size, got %q", content)
		}
	}
}
