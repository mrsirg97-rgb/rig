package guard_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/middleware/guard"
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

	if len(content) > 32 {
		t.Fatalf("a tiny cap must stay under the wall, got %d bytes: %q", len(content), content)
	}
	if content != strings.Repeat("e", 32) {
		t.Fatalf("below the marker the cut is bare: want the first 32 bytes of the content, got %q", content)
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

func TestACapBelowItsMarkerStaysUnderTheWall(t *testing.T) {
	full := strings.Repeat("m", 1000)
	marker := fmt.Sprintf("[TRUNCATED] full %d bytes, kept the head and the tail, the middle elided\nre-read a narrower range (read offset/limit)\n", len(full))
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return full, nil
	}
	for _, wall := range []int{1, len(marker) - 1} {
		out, err := guard.Cap(wall).Wrap(exec)(context.Background(), core.ToolCall{ID: "c", Name: "read", Args: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatalf("a cap below its marker must not fail the turn: %v", err)
		}
		if len(out) > wall {
			t.Fatalf("a cap below its marker must stay under the wall (%d bytes), got %d: %q", wall, len(out), out)
		}
		if !utf8.ValidString(out) {
			t.Fatalf("a cap below its marker must stay valid UTF-8, got %q", out)
		}
	}
}

func TestCapBelowTheMarkerCutsBare(t *testing.T) {
	full := strings.Repeat("abcdefgh", 64)
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return full, nil
	}
	out, err := guard.Cap(10).Wrap(exec)(context.Background(), core.ToolCall{ID: "c", Name: "read", Args: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("the cap must not fail the turn: %v", err)
	}
	if out != full[:10] {
		t.Fatalf("below the marker the cut is bare: want the first 10 bytes of the content, got %q", out)
	}
	if strings.HasPrefix(out, "[") {
		t.Fatalf("a partial marker must not be emitted, got %q", out)
	}
	wide := strings.Repeat("é", 100)
	out, err = guard.Cap(5).Wrap(func(ctx context.Context, call core.ToolCall) (string, error) {
		return wide, nil
	})(context.Background(), core.ToolCall{ID: "c2", Name: "read", Args: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("the cap must not fail the turn: %v", err)
	}
	if !utf8.ValidString(out) || len(out) > 5 {
		t.Fatalf("the bare cut must stay rune-safe under the wall, got %d bytes: %q", len(out), out)
	}
	if strings.HasPrefix(out, "[") {
		t.Fatalf("a partial marker must not be emitted, got %q", out)
	}
}
