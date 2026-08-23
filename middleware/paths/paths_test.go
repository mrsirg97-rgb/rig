package paths_test

import (
	"context"
	"encoding/json"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/paths"
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

func TestExpandTildeUserIsThatUsersHome(t *testing.T) {
	u, err := user.Current()
	if err != nil || u.Username == "" || u.HomeDir == "" {
		t.Skip("no current user to look up")
	}
	if got := paths.Expand("~" + u.Username + "/x"); got != filepath.Join(u.HomeDir, "x") {
		t.Fatalf("~user/x = %q, want %q", got, filepath.Join(u.HomeDir, "x"))
	}
	if got := paths.Expand("~" + u.Username); got != u.HomeDir {
		t.Fatalf("~user = %q, want %q", got, u.HomeDir)
	}
	if got := paths.Expand("~no-such-user-zz/x"); got != "~no-such-user-zz/x" {
		t.Fatalf("an unknown user stands as given, got %q", got)
	}
}

func TestExpandLeavesEverythingElseAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, in := range []string{"", ".", "/abs/~/x", "a/~", "rel/path"} {
		if got := paths.Expand(in); got != in {
			t.Fatalf("Expand(%q) = %q, want it untouched", in, got)
		}
	}
	t.Setenv("HOME", "")
	if got := paths.Expand("~/x"); got != "~/x" {
		t.Fatalf("with no home the path stands as given, got %q", got)
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
