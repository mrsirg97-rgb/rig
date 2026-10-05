package rem_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/middleware/index"
	"github.com/mrsirg97-rgb/rig/v2/store/graph"
	"github.com/mrsirg97-rgb/rig/v2/tool/file"
	remapi "github.com/mrsirg97-rgb/rig/v2/tool/rem"
)

const packAlpha = `package alpha

func Target() int { return Helper() }

func Helper() int { return 1 }
`

const packBeta = `package beta

import "example.com/m/alpha"

func Call() int { return alpha.Target() }
`

func packModule(t *testing.T, opts ...graph.Option) (string, *graph.Queue, core.Tool) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "gamma"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(root, "alpha", "a.go"):  packAlpha,
		filepath.Join(root, "beta", "b.go"):   packBeta,
		filepath.Join(root, "gamma", "g.go"):  "package gamma\n\nfunc Same() int { return 2 }\n",
		filepath.Join(root, "alpha", "g2.go"): "package alpha\n\nfunc Same() int { return 3 }\n",
	}
	for p, src := range files {
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	q := graph.NewQueue(t.TempDir(), nil, opts...)
	return root, q, remapi.New(newDB(t), q)
}

func graphModule(t *testing.T) (string, *graph.Queue, core.Tool) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	busy := "package core\n\nfunc gateOnce(todo string) bool {\n\tif todo == \"\" {\n\t\treturn false\n\t}\n\tfor i := 0; i < 3; i++ {\n\t\tif !contains(todo) {\n\t\t\treturn false\n\t\t}\n\t}\n\tif todo == \"x\" {\n\t\treturn todo + \"!\"\n\t}\n\tif todo == \"y\" {\n\t\treturn todo\n\t}\n\treturn true\n}\n\nfunc contains(s string) bool {\n\treturn s != \"\"\n}\n\nfunc Gate(todo string) bool { return gateOnce(todo) }\n"
	testFile := "package core_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/core\"\n)\n\nfunc contains(a, b string) bool { return a == b }\n\nfunc TestGate(t *testing.T) {\n\tif !contains(\"a\", \"a\") {\n\t\tt.Fatal(\"no\")\n\t}\n\tif !core.Gate(\"a\") {\n\t\tt.Fatal(\"gate\")\n\t}\n}\n"
	files := map[string]string{
		filepath.Join(root, "core", "busy.go"):      busy,
		filepath.Join(root, "core", "core_test.go"): testFile,
	}
	for p, src := range files {
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	q := graph.NewQueue(t.TempDir(), nil)
	return root, q, remapi.New(newDB(t), q)
}

func mapWithReads(t *testing.T, root string, q *graph.Queue, paths ...string) {
	t.Helper()
	read := index.Middleware(q).Wrap(func(ctx context.Context, call core.ToolCall) (string, error) {
		return file.NewRead().Exec(ctx, call.Args)
	})
	ctx := context.Background()
	for _, p := range paths {
		if _, err := read(ctx, core.ToolCall{Name: "read", Args: packJSON(t, map[string]any{"path": p})}); err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
	}
	q.Drain(ctx)
}

func packJSON(t *testing.T, args map[string]any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func packExec(t *testing.T, tool core.Tool, root, target string, extra map[string]any) (string, error) {
	t.Helper()
	args := map[string]any{"action": "pack", "target": target, "scope": root}
	for k, v := range extra {
		args[k] = v
	}
	payload, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Exec(context.Background(), payload)
}

func TestPackShowsDefinitionCallersAndSignaturesAfterExternalChange(t *testing.T) {
	root, q, tool := packModule(t)
	mapWithReads(t, root, q, filepath.Join(root, "alpha", "a.go"), filepath.Join(root, "beta", "b.go"))

	moved := "package alpha\n\n// the answer lives here now\n\nfunc Target() int { return Helper() + 42 }\n\nfunc Helper() int { return 1 }\n"
	if err := os.WriteFile(filepath.Join(root, "alpha", "a.go"), []byte(moved), 0o644); err != nil {
		t.Fatal(err)
	}
	newBeta := packBeta + "\nfunc Call2() int { return alpha.Target() }\n"
	if err := os.WriteFile(filepath.Join(root, "beta", "b.go"), []byte(newBeta), 0o644); err != nil {
		t.Fatal(err)
	}

	reply, err := packExec(t, tool, root, "alpha.Target", nil)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if !strings.Contains(reply, "alpha/a.go:5") {
		t.Fatalf("the definition did not move with the file:\n%s", reply)
	}
	if !strings.Contains(reply, "42") {
		t.Fatalf("the definition is not from the live file:\n%s", reply)
	}
	if !strings.Contains(reply, "beta.Call — beta/b.go:5") || !strings.Contains(reply, "beta.Call2 — beta/b.go:7") {
		t.Fatalf("the callers are not from the live map:\n%s", reply)
	}
	if !strings.Contains(reply, "alpha.Helper — func — alpha/a.go:7") {
		t.Fatalf("the callee's signature is missing:\n%s", reply)
	}
	if !strings.Contains(reply, "coverage:") {
		t.Fatalf("no coverage line:\n%s", reply)
	}
}

func TestPackRefusesAnAmbiguousBareName(t *testing.T) {
	root, q, tool := packModule(t)
	ctx := context.Background()
	if _, err := exec(t, tool, ctx, map[string]any{"action": "index", "scope": root}); err != nil {
		t.Fatalf("index: %v", err)
	}
	q.Drain(ctx)
	_, err := packExec(t, tool, root, "Same", nil)
	if err == nil {
		t.Fatal("the ambiguous bare name packed")
	}
	if !strings.Contains(err.Error(), "example.com/m/alpha") || !strings.Contains(err.Error(), "example.com/m/gamma") {
		t.Fatalf("the refusal does not name both places: %v", err)
	}
}

func TestPackRegistersNoObservation(t *testing.T) {
	root, q, tool := packModule(t)
	mapWithReads(t, root, q, filepath.Join(root, "alpha", "a.go"))
	session := core.NewSession()
	session.Files[filepath.Join(root, "alpha", "a.go")] = core.FileState{Hash: "x", Mtime: 1}
	_, err := packExec(t, tool, root, "alpha.Target", nil)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if len(session.Files) != 1 {
		t.Fatalf("pack wrote observations: %v", session.Files)
	}
}

func TestIndexMapsTheWholeProject(t *testing.T) {
	root, q, tool := packModule(t)
	ctx := context.Background()
	reply, err := exec(t, tool, ctx, map[string]any{"action": "index", "scope": root})
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if !strings.Contains(reply, "mapped 4") {
		t.Fatalf("index reply: %s", reply)
	}
	q.Close()
	packed, err := packExec(t, tool, root, "alpha.Helper", nil)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if !strings.Contains(packed, "coverage: 3 of 3 packages mapped") {
		t.Fatalf("coverage after index:\n%s", packed)
	}
	if _, err := packExec(t, tool, root, "Nope", nil); err == nil {
		t.Fatal("a symbol off the map packed")
	}
}

func TestPackRefusesAnAmbiguousNameAcrossTestPackages(t *testing.T) {
	root, q, tool := graphModule(t)
	ctx := context.Background()
	if _, err := exec(t, tool, ctx, map[string]any{"action": "index", "scope": root}); err != nil {
		t.Fatalf("index: %v", err)
	}
	q.Close()
	_, err := packExec(t, tool, root, "contains", nil)
	if err == nil {
		t.Fatal("the bare name spanning both packages packed")
	}
	if !strings.Contains(err.Error(), "example.com/m/core") || !strings.Contains(err.Error(), "example.com/m/core_test") {
		t.Fatalf("the refusal does not name both packages: %v", err)
	}
	packed, err := packExec(t, tool, root, "core.contains", nil)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if !strings.Contains(packed, "contains — func — core/busy.go:21") {
		t.Fatalf("the package-qualified name must resolve to busy.go's contains:\n%s", packed)
	}
}

func TestPackGateOnceShowsExactlyItsSeventeenLines(t *testing.T) {
	root, q, tool := graphModule(t)
	ctx := context.Background()
	if _, err := exec(t, tool, ctx, map[string]any{"action": "index", "scope": root}); err != nil {
		t.Fatalf("index: %v", err)
	}
	q.Close()
	reply, err := packExec(t, tool, root, "core.gateOnce", nil)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	cut := strings.Index(reply, "\ncallers")
	if cut < 0 {
		t.Fatalf("no callers section:\n%s", reply)
	}
	def := strings.Split(reply[:cut], "\n")[1:]
	if len(def) != 17 {
		t.Fatalf("the definition window holds %d lines, want exactly 17:\n%s", len(def), reply)
	}
	if !strings.Contains(def[0], "3 func gateOnce(todo string) bool {") || !strings.Contains(def[16], "19 }") {
		t.Fatalf("the window is not gateOnce's lines 3..19:\n%s", reply)
	}
	if !strings.Contains(reply, "callees (1):") || !strings.Contains(reply, "core.contains — func — core/busy.go:21") {
		t.Fatalf("the callee is not busy.go's contains:\n%s", reply)
	}
	if !strings.Contains(reply, "    21 func contains(s string) bool {") {
		t.Fatalf("the callee's signature line is missing:\n%s", reply)
	}
	if strings.Contains(reply, "    22 ") {
		t.Fatalf("the callee shows past the opening brace:\n%s", reply)
	}
}

func TestPackArgsCarryTheWireShape(t *testing.T) {
	_, _, tool := packModule(t)
	var schema struct {
		Props map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Props["target"].Description == "" {
		t.Fatal("the target field carries no description")
	}
}

func TestPackTeachesTheQualifierShape(t *testing.T) {
	root, q, tool := packModule(t)
	ctx := context.Background()
	if _, err := exec(t, tool, ctx, map[string]any{"action": "index", "scope": root}); err != nil {
		t.Fatalf("index: %v", err)
	}
	q.Drain(ctx)
	_, err := packExec(t, tool, root, "example.com/m/alpha.Helper", nil)
	if err == nil || !strings.Contains(err.Error(), "import path") || !strings.Contains(err.Error(), "alpha.Helper") {
		t.Fatalf("an import path must refuse naming the package tail, got %v", err)
	}
	_, err = packExec(t, tool, root, "beta.Helper", nil)
	if err == nil || !strings.Contains(err.Error(), "no Helper in package beta") || !strings.Contains(err.Error(), "alpha.Helper") {
		t.Fatalf("a wrong package tail must name where the map has the symbol, got %v", err)
	}
	_, err = packExec(t, tool, root, "alpha/a.go", nil)
	if err != nil {
		t.Fatalf("a real file path still packs the file: %v", err)
	}
}
