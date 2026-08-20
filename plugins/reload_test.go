// The reload's named cases (SPEC_PLUGINS 8, testing): the leaf, fake
// kernel — no python required. The e2e (real kernel) lives in cmd/rig.
package plugins

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pythontool "github.com/mrsirg97-rgb/rig/tool/python"
)

// TestReloadToolSurfacesAreTheNativeContract (SPEC_PLUGINS 8, named):
// Name is plugins_reload, the schema the empty object, the description
// names the re-discovery and the next-turn effect.
func TestReloadToolSurfacesAreTheNativeContract(t *testing.T) {
	tool := NewReload("/h", map[string]bool{"bash": true, "plugins_reload": true}, &fakeKernel{}, func(ctx context.Context, reports []Report) (string, error) {
		return "plugins: reload: 0 loaded, 0 skipped", nil
	})
	if tool.Name() != "plugins_reload" {
		t.Fatalf("Name = %q, want plugins_reload (a native tool)", tool.Name())
	}
	if string(tool.Schema()) != `{"type":"object"}` {
		t.Fatalf("the schema = %s, want the empty object (no arguments)", tool.Schema())
	}
	desc := tool.Description()
	if !strings.Contains(desc, "discovery") || !strings.Contains(desc, "next turn") {
		t.Fatalf("the description must name the re-discovery and the next-turn effect, got %q", desc)
	}
}

// TestReloadToolExecRediscoversAndHandsOff (SPEC_PLUGINS 8, named): a
// canned report (one loaded, one skipped) — the swap receives the
// reports in file order, the reply is the swap's verbatim, and the
// kernel's cell is the discovery's (the files embedded).
func TestReloadToolExecRediscoversAndHandsOff(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	echoFile := filepath.Join(home, "plugins", "echo.py")
	brokenFile := filepath.Join(home, "plugins", "broken.py")
	for _, f := range []string{echoFile, brokenFile} {
		if err := os.WriteFile(f, []byte("x = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	report := `[{"name":"broken","file":"` + brokenFile + `","ok":false,"error":"NameError: name 'x' is not defined"},{"name":"echo","file":"` + echoFile + `","ok":true,"description":"echoes","schema":{"type":"object"}}]`
	k := &fakeKernel{replies: []pythontool.Reply{okReply(report + "\n")}}
	var got []Report
	swap := func(ctx context.Context, reports []Report) (string, error) {
		got = reports
		return "plugins: reload: 1 loaded, 1 skipped", nil
	}
	tool := NewReload(home, map[string]bool{"bash": true}, k, swap)
	out, err := tool.Exec(context.Background(), nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if out != "plugins: reload: 1 loaded, 1 skipped" {
		t.Fatalf("the reply = %q, want the swap's verbatim", out)
	}
	if len(got) != 2 || got[0].Name != "broken" || !got[0].Skipped || got[1].Name != "echo" || got[1].Skipped {
		t.Fatalf("the swap's reports = %+v, want the discovery's in file order", got)
	}
	if len(k.cells) != 1 {
		t.Fatalf("cells = %d, want 1 (the discovery's)", len(k.cells))
	}
	for _, want := range []string{echoFile, brokenFile} {
		if !strings.Contains(k.cells[0], want) {
			t.Fatalf("the discovery cell must embed the file %s:\n%s", want, k.cells[0])
		}
	}
}

// TestReloadToolEmptyDirectoryNeverStartsTheKernel (SPEC_PLUGINS 8,
// named): no top-level file — zero kernel cells, the swap receives the
// empty report (the list rebuilds to the natives — removal free), the
// reply names the empty list. Both shapes: an empty directory, and a
// home without the directory at all.
func TestReloadToolEmptyDirectoryNeverStartsTheKernel(t *testing.T) {
	for _, home := range []string{t.TempDir(), emptyPluginsHome(t)} {
		k := &fakeKernel{}
		called := false
		tool := NewReload(home, map[string]bool{}, k, func(ctx context.Context, reports []Report) (string, error) {
			called = true
			if len(reports) != 0 {
				t.Fatalf("the empty listing's swap got %d reports, want 0", len(reports))
			}
			return "plugins: reload: 0 loaded, 0 skipped", nil
		})
		out, err := tool.Exec(context.Background(), nil)
		if err != nil || out != "plugins: reload: 0 loaded, 0 skipped" {
			t.Fatalf("(out, err) = (%q, %v), want the empty list (removal free)", out, err)
		}
		if !called {
			t.Fatal("the swap must run (the list rebuilds to the natives)")
		}
		if len(k.cells) != 0 {
			t.Fatalf("the empty listing started the kernel (cells = %d)", len(k.cells))
		}
	}
}

// emptyPluginsHome is a scratch home with no plugins/ directory.
func emptyPluginsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestReloadToolCollisionRefusesBeforeTheSwap (SPEC_PLUGINS 8, named):
// a loaded report named like a native (the set includes plugins_reload
// itself) — the refusal is the startup collision's voice, and the swap
// is never called.
func TestReloadToolCollisionRefusesBeforeTheSwap(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bash", "plugins_reload"} {
		file := filepath.Join(home, "plugins", name+".py")
		if err := os.WriteFile(file, []byte("x = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		report := `[{"name":"` + name + `","file":"` + file + `","ok":true,"description":"shadow","schema":{"type":"object"}}]`
		k := &fakeKernel{replies: []pythontool.Reply{okReply(report)}}
		called := false
		tool := NewReload(home, map[string]bool{"bash": true, "plugins_reload": true}, k, func(ctx context.Context, reports []Report) (string, error) {
			called = true
			return "plugins: reload: 1 loaded, 0 skipped", nil
		})
		_, err := tool.Exec(context.Background(), nil)
		if err == nil {
			t.Fatalf("%s.py must refuse (a native name)", name)
		}
		want := `plugins: name collision: "` + name + `" (` + name + `.py) is already a native tool`
		if err.Error() != want {
			t.Fatalf("the refusal = %q, want the startup collision's voice:\n%q", err.Error(), want)
		}
		if called {
			t.Fatalf("the swap ran on a refused reload (the list would have changed)")
		}
	}
}

// TestReloadToolKernelFailureIsTheError (SPEC_PLUGINS 8, named): a
// non-OK reply — the error carries the kernel's reason under the
// reload's prefix, and the swap is never called.
func TestReloadToolKernelFailureIsTheError(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "plugins", "echo.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k := &fakeKernel{replies: []pythontool.Reply{errReply("kernel exited (code 1)", "")}}
	called := false
	tool := NewReload(home, map[string]bool{}, k, func(ctx context.Context, reports []Report) (string, error) {
		called = true
		return "plugins: reload: 0 loaded, 0 skipped", nil
	})
	_, err := tool.Exec(context.Background(), nil)
	if err == nil {
		t.Fatal("a discovery failure must be the error")
	}
	if !strings.Contains(err.Error(), "kernel exited (code 1)") {
		t.Fatalf("the error must carry the kernel's reason, got %v", err)
	}
	if !strings.Contains(err.Error(), "plugins: reload:") {
		t.Fatalf("the reason rides under the reload's prefix, got %v", err)
	}
	if called {
		t.Fatal("the swap ran on a failed discovery (the list would have changed)")
	}
}

// TestListIsTopLevelPyOnly (SPEC_PLUGINS 8, named): the listing rule —
// the pending zone, a subdirectory, and a non-.py file are not the
// listing's, and the order is filename order. No directory, or an empty
// one, is the no-op (the kernel's never-starting, 2's rule).
func TestListIsTopLevelPyOnly(t *testing.T) {
	home := t.TempDir()
	plugins := filepath.Join(home, "plugins")
	paths := []string{
		filepath.Join(plugins, "b.py"),
		filepath.Join(plugins, "a.py"),
		filepath.Join(plugins, "pending", "inner.py"),
		filepath.Join(plugins, "sub", "deep.py"),
		filepath.Join(plugins, "notes.md"),
	}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := List(home)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{filepath.Join(plugins, "a.py"), filepath.Join(plugins, "b.py")}
	if len(files) != len(want) {
		t.Fatalf("the listing = %v, want the top-level *.py only:\n%v", files, want)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Fatalf("the listing = %v, want the filename order:\n%v", files, want)
		}
	}

	for _, h := range []string{t.TempDir(), emptyPluginsHome(t)} {
		files, err := List(h)
		if err != nil || files != nil {
			t.Fatalf("(files, err) = (%v, %v), want the no-op (nil, nil)", files, err)
		}
	}
}

// TestCheckVoicesTheCollision (SPEC_PLUGINS 8, named): the shared
// refusal (the startup's and the reload's one rule) — the voice, and a
// skipped report never collides (it is not a tool).
func TestCheckVoicesTheCollision(t *testing.T) {
	natives := map[string]bool{"bash": true, "plugins_reload": true}
	report := Report{Name: "bash", File: "/h/plugins/bash.py", Skipped: false}
	err := Check([]Report{report}, natives)
	if err == nil {
		t.Fatal("a loaded report named like a native must refuse")
	}
	if want := `plugins: name collision: "bash" (bash.py) is already a native tool`; err.Error() != want {
		t.Fatalf("the voice = %q, want %q", err.Error(), want)
	}

	skipped := Report{Name: "bash", File: "/h/plugins/bash.py", Skipped: true, Reason: "missing run"}
	if err := Check([]Report{skipped}, natives); err != nil {
		t.Fatalf("a skipped report never collides, got %v", err)
	}
	if err := Check(nil, natives); err != nil {
		t.Fatalf("the empty report never collides, got %v", err)
	}
}
