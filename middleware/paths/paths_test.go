package paths_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/paths"
	"github.com/mrsirg97-rgb/rig/tool/bash"
	"github.com/mrsirg97-rgb/rig/tool/file"
	"github.com/mrsirg97-rgb/rig/tool/fs"
)

func TestExpandLeadingTildeIsTheHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for in, want := range map[string]string{
		"~":                home,
		"~/":               home,
		"~/Projects/hedge": filepath.Join(home, "Projects", "hedge"),
		"~/a/../b":         filepath.Join(home, "b"),
	} {
		if got := paths.Expand(in); got != want {
			t.Fatalf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpandTildeUserStandsAsGiven(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := paths.Expand("~root/x"); got != "~root/x" {
		t.Fatalf("a ~user path must stand as given, got %q", got)
	}
	if got := paths.Expand("~root"); got != "~root" {
		t.Fatalf("a bare ~user must stand as given, got %q", got)
	}
}

func TestExpandLeavesEverythingElseAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, in := range []string{"", ".", "/abs/~/x", "a/~", "a/~/b", "rel/path"} {
		if got := paths.Expand(in); got != in {
			t.Fatalf("Expand(%q) = %q, want it untouched", in, got)
		}
	}
	t.Setenv("HOME", "")
	if got := paths.Expand("~/x"); got != "~/x" {
		t.Fatalf("with no home the path stands as given, got %q", got)
	}
}

func TestToolsExpandTheLeadingTildeAtTheBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := filepath.Join(home, "proj")
	if err := os.Mkdir(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	inner := func(ctx context.Context, call core.ToolCall) (string, error) {
		switch call.Name {
		case "bash":
			return bash.New().Exec(ctx, call.Args)
		case "ls":
			return fs.LS().Exec(ctx, call.Args)
		case "read":
			return file.Read().Exec(ctx, call.Args)
		default:
			return "", errors.New("unexpected tool: " + call.Name)
		}
	}
	exec := paths.Middleware().Wrap(inner)

	got, err := exec(context.Background(), core.ToolCall{Name: "bash", Args: json.RawMessage(`{"command":"pwd","cwd":"~/proj"}`)})
	if err != nil || got != proj+"\n" {
		t.Fatalf("bash's cwd must expand to the home: %q, %v", got, err)
	}
	got, err = exec(context.Background(), core.ToolCall{Name: "ls", Args: json.RawMessage(`{"path":"~/proj"}`)})
	if err != nil || !strings.Contains(got, "a.txt") {
		t.Fatalf("ls's path must expand to the home: %q, %v", got, err)
	}
	got, err = exec(context.Background(), core.ToolCall{Name: "read", Args: json.RawMessage(`{"path":"~/proj/a.txt"}`)})
	if err != nil || got != "hi" {
		t.Fatalf("read's path must expand to the home: %q, %v", got, err)
	}
}

func TestMiddlewareRewritesOnlyThePathFieldsAtTheBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var seen core.ToolCall
	exec := paths.Middleware().Wrap(func(ctx context.Context, call core.ToolCall) (string, error) {
		seen = call
		return "ok", nil
	})
	in := core.ToolCall{ID: "c1", Name: "edit", Args: json.RawMessage(`{"path":"~/a.txt","old":"~/keep","new":"~","root":"~/r","cwd":"~","content":"~/c","n":3}`)}
	if _, err := exec(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(seen.Args, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"path": filepath.Join(home, "a.txt"), "root": filepath.Join(home, "r"), "cwd": home,
		"old": "~/keep", "new": "~", "content": "~/c", "n": float64(3),
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %v, want %v", k, got[k], v)
		}
	}
	if seen.ID != "c1" || seen.Name != "edit" {
		t.Fatalf("the call's identity must ride through: %+v", seen)
	}
}

func TestMiddlewareLeavesBytesAloneWhenNothingExpands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, raw := range []string{`{"path":"/abs/x","cwd":"rel","z":"~/not-a-path"}`, `{"path":5}`, `[1,2]`, `null`, `not json`, `{}`} {
		var seen core.ToolCall
		exec := paths.Middleware().Wrap(func(ctx context.Context, call core.ToolCall) (string, error) {
			seen = call
			return "ok", nil
		})
		if _, err := exec(context.Background(), core.ToolCall{Name: "read", Args: json.RawMessage(raw)}); err != nil {
			t.Fatal(err)
		}
		if string(seen.Args) != raw {
			t.Fatalf("args %s must pass byte-identical when nothing expands, got %s", raw, seen.Args)
		}
	}
}
