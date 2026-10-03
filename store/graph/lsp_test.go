package graph_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/store/graph"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
)

const fakeTestName = "TestFakeLSPServer"

func fakeServer(t *testing.T, lang, log, syms, refs string) []string {
	t.Helper()
	return []string{"env",
		"RIG_FAKE_LSP=1",
		"RIG_FAKE_LSP_LOG=" + log,
		"RIG_FAKE_LSP_SYMBOLS=" + syms,
		"RIG_FAKE_LSP_REFS=" + refs,
		os.Args[0], "-test.run=" + fakeTestName,
	}
}

func fakeLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func logCount(path, method string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(b), method+"\n")
}

func writeFake(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

var widgetSymbols = []any{
	map[string]any{
		"name":  "Widget",
		"kind":  5,
		"range": map[string]any{"start": map[string]any{"line": 2, "character": 6}, "end": map[string]any{"line": 6, "character": 1}},
	},
	map[string]any{
		"name":  "draw",
		"kind":  6,
		"range": map[string]any{"start": map[string]any{"line": 5, "character": 2}, "end": map[string]any{"line": 5, "character": 11}},
	},
}

func lspProject(t *testing.T) (string, *graph.Queue) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(root, "src", "widget.ts"): "export class Widget {\n  draw() {}\n}\n",
		filepath.Join(root, "lib", "other.ts"):  "import { Widget } from '../src/widget';\n\nconst w = new Widget();\n",
		filepath.Join(root, "third.py"):         "class Widget:\n    pass\n",
	}
	for p, src := range files {
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, graph.NewQueue(t.TempDir(), nil)
}

func TestReadOfNonGoFileStartsTheServerOnce(t *testing.T) {
	root, q := lspProject(t)
	defer q.Close()
	log := fakeLog(t)
	syms := filepath.Join(t.TempDir(), "syms.json")
	writeFake(t, syms, widgetSymbols)
	graph.SetServer("typescript", fakeServer(t, "typescript", log, syms, "")...)
	t.Cleanup(func() { graph.SetServer("typescript") })
	q.Touch(filepath.Join(root, "src", "widget.ts"))
	q.Drain(context.Background())
	q.Touch(filepath.Join(root, "lib", "other.ts"))
	q.Drain(context.Background())
	if got := logCount(log, "initialize"); got != 1 {
		t.Fatalf("initialize count = %d, want 1 (log %s)", got, log)
	}
	if got := logCount(log, "textDocument/documentSymbol"); got != 2 {
		t.Fatalf("documentSymbol count = %d, want 2", got)
	}
	db, err := graph.Open(q.Home(), scope.Key(root), scope.Worktree(root))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	var kinds int
	if err := db.QueryRow("SELECT count(*) FROM symbols WHERE name = 'Widget' AND kind = 'type'").Scan(&kinds); err != nil {
		t.Fatal(err)
	}
	var endLine int64
	if err := db.QueryRow("SELECT end_line FROM symbols WHERE name = 'Widget' AND kind = 'type'").Scan(&endLine); err != nil {
		t.Fatal(err)
	}
	if endLine != 7 {
		t.Fatalf("Widget ends on line %d, want 7 (the range's end line)", endLine)
	}
	if kinds != 2 {
		t.Fatalf("Widget type rows = %d, want 2 (one per file)", kinds)
	}
}

func TestSecondLanguageReplacesTheServer(t *testing.T) {
	root, q := lspProject(t)
	defer q.Close()
	tsLog, pyLog := fakeLog(t), fakeLog(t)
	empty := filepath.Join(t.TempDir(), "empty.json")
	writeFake(t, empty, []any{})
	syms := filepath.Join(t.TempDir(), "syms.json")
	writeFake(t, syms, widgetSymbols)
	graph.SetServer("typescript", fakeServer(t, "typescript", tsLog, syms, "")...)
	graph.SetServer("python", fakeServer(t, "python", pyLog, empty, "")...)
	t.Cleanup(func() {
		graph.SetServer("typescript")
		graph.SetServer("python")
	})
	ctx := context.Background()
	q.Touch(filepath.Join(root, "src", "widget.ts"))
	q.Drain(ctx)
	q.Touch(filepath.Join(root, "third.py"))
	q.Drain(ctx)
	if got := logCount(pyLog, "initialize"); got != 1 {
		t.Fatalf("python initialize count = %d, want 1", got)
	}
	b, err := os.ReadFile(tsLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "eof") {
		t.Fatalf("the typescript server was not replaced:\n%s", b)
	}
}

func TestDocumentSymbolsLandAndReferencesResolveOnPack(t *testing.T) {
	root, q := lspProject(t)
	defer q.Close()
	log := fakeLog(t)
	syms := filepath.Join(t.TempDir(), "syms.json")
	writeFake(t, syms, widgetSymbols)
	refs := filepath.Join(t.TempDir(), "refs.json")
	writeFake(t, refs, []any{
		map[string]any{
			"uri":   "file://" + filepath.Join(root, "lib", "other.ts"),
			"range": map[string]any{"start": map[string]any{"line": 9, "character": 12}},
		},
	})
	graph.SetServer("typescript", fakeServer(t, "typescript", log, syms, refs)...)
	t.Cleanup(func() { graph.SetServer("typescript") })
	ctx := context.Background()
	q.Touch(filepath.Join(root, "src", "widget.ts"))
	q.Touch(filepath.Join(root, "lib", "other.ts"))
	q.Drain(ctx)

	packed, err := q.Pack(ctx, root, "src.Widget")
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if !strings.Contains(packed, "Widget — type — src/widget.ts:3") {
		t.Fatalf("the definition is wrong:\n%s", packed)
	}
	if !strings.Contains(packed, "lib.draw — lib/other.ts:10") {
		t.Fatalf("the reference did not land as a caller:\n%s", packed)
	}
	if got := logCount(log, "textDocument/references"); got != 1 {
		t.Fatalf("references count = %d, want 1", got)
	}
	if packed, err = q.Pack(ctx, root, "src.Widget"); err != nil {
		t.Fatalf("pack: %v", err)
	}
	if got := logCount(log, "textDocument/references"); got != 1 {
		t.Fatalf("the second pack was not cached by sha: references count %d", got)
	}
	shifted := "import { Widget } from '../src/widget';\n\n// moved\n\nexport class Widget {\n  draw() {}\n}\n"
	if err := os.WriteFile(filepath.Join(root, "src", "widget.ts"), []byte(shifted), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = q.Pack(ctx, root, "src.Widget"); err != nil {
		t.Fatalf("pack: %v", err)
	}
	if got := logCount(log, "textDocument/references"); got != 2 {
		t.Fatalf("the moved sha did not re-resolve: references count %d", got)
	}
}
