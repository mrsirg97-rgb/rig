package rem_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
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

func packModule(t *testing.T) (string, *graph.Queue, core.Tool) {
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
	q := graph.NewQueue(t.TempDir(), nil)
	return root, q, remapi.New(newDB(t), q)
}

func mapWithReads(t *testing.T, root string, q *graph.Queue, paths ...string) {
	t.Helper()
	file.SetIndexer(q)
	t.Cleanup(func() { file.SetIndexer(nil) })
	ctx := context.Background()
	for _, p := range paths {
		if _, err := file.Read().Exec(ctx, packJSON(t, map[string]any{"path": p})); err != nil {
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
	args := map[string]any{"action": "pack", "target": target, "project": root}
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
	if _, err := exec(t, tool, ctx, map[string]any{"action": "index", "project": root}); err != nil {
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
	reply, err := exec(t, tool, ctx, map[string]any{"action": "index", "project": root})
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if !strings.Contains(reply, "indexing") {
		t.Fatalf("index reply: %s", reply)
	}
	q.Drain(ctx)
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
