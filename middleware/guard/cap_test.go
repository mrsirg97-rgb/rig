package guard_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/guard"
)

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

	if !strings.HasPrefix(content, "h") || !strings.HasSuffix(content, "t") {
		t.Fatalf("the head and the tail must be kept, got %q", content)
	}
	if strings.Contains(content, strings.Repeat("m", 3)) {
		t.Fatalf("the middle must be elided, got %q", content)
	}

	for _, want := range []string{"[TRUNCATED]", "1200", "head", "tail", "re-read a narrower range"} {
		if !strings.Contains(content, want) {
			t.Fatalf("the marker must name the full size and the teaching line, got %q", content)
		}
	}
}

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

	if strings.Contains(content, "eee") {
		t.Fatalf("a tiny cap must elide the content whole, got %q", content)
	}
	for _, want := range []string{"[TRUNCATED]", "2000", "re-read a narrower range"} {
		if !strings.Contains(content, want) {
			t.Fatalf("the marker must survive a tiny cap and name the size, got %q", content)
		}
	}
}

func TestCapCutsOnRuneBoundaries(t *testing.T) {
	content := strings.Repeat("é", 400)
	exec := guard.Cap(200).Wrap(func(ctx context.Context, call core.ToolCall) (string, error) { return content, nil })
	out, _ := exec(context.Background(), core.ToolCall{Name: "read"})
	if !utf8.ValidString(out) {
		t.Fatalf("the truncation produced invalid UTF-8")
	}
	if !strings.Contains(out, "[TRUNCATED]") {
		t.Fatal("the marker is missing")
	}
}
