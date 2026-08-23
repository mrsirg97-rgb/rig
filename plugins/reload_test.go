package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pythontool "github.com/mrsirg97-rgb/rig/tool/python"
)

func TestEcosystemSurfacesAreTheNativeContract(t *testing.T) {
	tool := NewEcosystem("/h", map[string]bool{"bash": true, "plugins": true}, &fakeKernel{}, func(ctx context.Context, reports []Report) (string, error) {
		return "plugins: reload: 0 loaded, 0 skipped", nil
	}, func() (string, error) {
		return "plugins: none", nil
	})
	if tool.Name() != "plugins" {
		t.Fatalf("Name = %q, want plugins (a native tool)", tool.Name())
	}
	var params struct {
		Properties struct {
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema(), &params); err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
	if len(params.Properties.Action.Enum) != 4 || params.Properties.Action.Enum[0] != "list" || params.Properties.Action.Enum[1] != "create" || params.Properties.Action.Enum[2] != "delete" || params.Properties.Action.Enum[3] != "reload" {
		t.Fatalf("the action enum = %v, want list, create, delete, reload", params.Properties.Action.Enum)
	}
	desc := tool.Description()
	if !strings.Contains(desc, "list") || !strings.Contains(desc, "create") || !strings.Contains(desc, "delete") || !strings.Contains(desc, "reload") {
		t.Fatalf("the description must name the four ecosystem verbs, got %q", desc)
	}
}

func TestEcosystemExecReloadRediscoversAndHandsOff(t *testing.T) {
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
	tool := NewEcosystem(home, map[string]bool{"bash": true}, k, swap, nil)
	out, err := tool.Exec(context.Background(), json.RawMessage(`{"action":"reload"}`))
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

func TestEcosystemExecListReadsTheSeam(t *testing.T) {
	tool := NewEcosystem("/h", map[string]bool{}, &fakeKernel{}, nil, func() (string, error) {
		return "plugins: 2 loaded, 0 skipped", nil
	})
	out, err := tool.Exec(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil || out != "plugins: 2 loaded, 0 skipped" {
		t.Fatalf("the list = (%q, %v), want the listing seam's verbatim", out, err)
	}
	missing := NewEcosystem("/h", map[string]bool{}, &fakeKernel{}, nil, nil)
	_, err = missing.Exec(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err == nil || !strings.Contains(err.Error(), "no listing seam") {
		t.Fatalf("a missing list seam must refuse by name, got %v", err)
	}
}

func TestEcosystemExecCreateWritesPending(t *testing.T) {
	home := t.TempDir()
	tool := NewEcosystem(home, map[string]bool{"bash": true, "plugins": true}, &fakeKernel{}, nil, nil)
	out, err := tool.Exec(context.Background(), json.RawMessage(`{"action":"create","name":"echo","source":"DESCRIPTION = \"x\"\nSCHEMA = {}\ndef run(args): return \"x\"\n"}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(out, "plugins: create: wrote echo") {
		t.Fatalf("the create reply = %q", out)
	}
	path := filepath.Join(home, "plugins", "pending", "echo.py")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the created plugin must land in plugins/pending/: %v", err)
	}
	for _, bad := range []string{`{"action":"create","name":"","source":"x"}`, `{"action":"create","name":"a/b","source":"x"}`, `{"action":"create","name":"bash","source":"x"}`, `{"action":"create","name":"plugins","source":"x"}`, `{"action":"create","name":"echo","source":"  "}`} {
		_, err := tool.Exec(context.Background(), json.RawMessage(bad))
		if err == nil {
			t.Fatalf("create %s must refuse", bad)
		}
	}
}

func TestEcosystemExecDeleteRemovesTheLoaded(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "echo.py")
	if err := os.WriteFile(path, []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewEcosystem(home, map[string]bool{}, &fakeKernel{}, nil, nil)
	out, err := tool.Exec(context.Background(), json.RawMessage(`{"action":"delete","name":"echo"}`))
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(out, "plugins: delete: removed echo") {
		t.Fatalf("the delete reply = %q", out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the loaded plugin must be gone: %v", err)
	}
	_, err = tool.Exec(context.Background(), json.RawMessage(`{"action":"delete","name":"echo"}`))
	if err == nil || !strings.Contains(err.Error(), "no plugin \"echo\"") {
		t.Fatalf("a second delete must refuse by name, got %v", err)
	}
}

func TestEcosystemExecUnknownActionRefuses(t *testing.T) {
	tool := NewEcosystem("/h", map[string]bool{}, &fakeKernel{}, nil, nil)
	_, err := tool.Exec(context.Background(), json.RawMessage(`{"action":"sideways"}`))
	if err == nil || !strings.Contains(err.Error(), `unknown action "sideways"`) {
		t.Fatalf("an unknown action must refuse by name, got %v", err)
	}
}

func TestEcosystemReloadEmptyDirectoryNeverStartsTheKernel(t *testing.T) {
	for _, home := range []string{t.TempDir(), emptyPluginsHome(t)} {
		k := &fakeKernel{}
		called := false
		tool := NewEcosystem(home, map[string]bool{}, k, func(ctx context.Context, reports []Report) (string, error) {
			called = true
			if len(reports) != 0 {
				t.Fatalf("the empty listing's swap got %d reports, want 0", len(reports))
			}
			return "plugins: reload: 0 loaded, 0 skipped", nil
		}, nil)
		out, err := tool.Exec(context.Background(), json.RawMessage(`{"action":"reload"}`))
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

func emptyPluginsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestEcosystemCollisionRefusesBeforeTheSwap(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bash", "plugins"} {
		file := filepath.Join(home, "plugins", name+".py")
		if err := os.WriteFile(file, []byte("x = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		report := `[{"name":"` + name + `","file":"` + file + `","ok":true,"description":"shadow","schema":{"type":"object"}}]`
		k := &fakeKernel{replies: []pythontool.Reply{okReply(report)}}
		called := false
		tool := NewEcosystem(home, map[string]bool{"bash": true, "plugins": true}, k, func(ctx context.Context, reports []Report) (string, error) {
			called = true
			return "plugins: reload: 1 loaded, 0 skipped", nil
		}, nil)
		_, err := tool.Exec(context.Background(), json.RawMessage(`{"action":"reload"}`))
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

func TestEcosystemKernelFailureIsTheError(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "plugins", "echo.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k := &fakeKernel{replies: []pythontool.Reply{errReply("kernel exited (code 1)", "")}}
	called := false
	tool := NewEcosystem(home, map[string]bool{}, k, func(ctx context.Context, reports []Report) (string, error) {
		called = true
		return "plugins: reload: 0 loaded, 0 skipped", nil
	}, nil)
	_, err := tool.Exec(context.Background(), json.RawMessage(`{"action":"reload"}`))
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

func TestCheckVoicesTheCollision(t *testing.T) {
	natives := map[string]bool{"bash": true, "plugins": true}
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

func TestZonesAreDirectoriesAndListSkipsThem(t *testing.T) {
	home := t.TempDir()
	for _, p := range []string{"plugins/live.py", "plugins/pending/draft.py", "plugins/disabled/off.py"} {
		full := filepath.Join(home, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("DESCRIPTION = \"x\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	live, err := List(home)
	if err != nil || len(live) != 1 || filepath.Base(live[0]) != "live.py" {
		t.Fatalf("List = %v, %v; want live.py only", live, err)
	}
	off, err := Zone(home, "disabled")
	if err != nil || len(off) != 1 || filepath.Base(off[0]) != "off.py" {
		t.Fatalf("Zone(disabled) = %v, %v", off, err)
	}
	none, err := Zone(home, "nope")
	if err != nil || len(none) != 0 {
		t.Fatalf("an absent zone is empty, not an error: %v, %v", none, err)
	}
}
