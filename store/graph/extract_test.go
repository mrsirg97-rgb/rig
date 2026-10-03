package graph_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/graph"
)

const alphaSrc = `package alpha

type Rec struct{ N int }

func (r Rec) Move(d int) int { return r.N + d }

func Alpha() int { return 1 }

const Limit = 7

var Shared = 3
`

const betaSrc = `package beta

import "example.com/m/alpha"

func Caller() int {
	r := alpha.Rec{N: 1}
	return r.Move(2) + alpha.Alpha() + alpha.Limit + alpha.Shared
}
`

const brokenSrc = `package gamma

import "example.com/m/nosuch"

func Here() int { return also() + nosuch.Thing }

func also() int { return 1 }
`

func writeModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n\ngo 1.26.6\n"), 0o644); err != nil {
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
	if err := os.MkdirAll(filepath.Join(root, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(root, "alpha", "a.go"):  alphaSrc,
		filepath.Join(root, "beta", "b.go"):   betaSrc,
		filepath.Join(root, "gamma", "g.go"):  brokenSrc,
		filepath.Join(root, "alpha", "x.go"):  "package alpha\n\nfunc other() int { return 2 }\n",
		filepath.Join(root, "vendor", "v.go"): "package vendor\n",
	}
	for p, src := range files {
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func shaFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func extract(t *testing.T, abs string) graph.Result {
	t.Helper()
	res, err := new(graph.GoExtract).Extract(context.Background(), abs)
	if err != nil {
		t.Fatalf("extract %s: %v", abs, err)
	}
	return res
}

func TestGoExtractMapsThePackageSymbols(t *testing.T) {
	root := writeModule(t)
	res := extract(t, filepath.Join(root, "alpha", "a.go"))
	want := map[string]string{
		"Alpha":    "func",
		"Rec":      "type",
		"Rec.Move": "method",
		"Limit":    "const",
		"Shared":   "var",
	}
	if len(res.Symbols) != len(want) {
		t.Fatalf("symbols = %v, want exactly %v", res.Symbols, want)
	}
	for _, s := range res.Symbols {
		if s.Package != "example.com/m/alpha" {
			t.Fatalf("symbol %s carries package %q, want example.com/m/alpha", s.Name, s.Package)
		}
		if s.File != "alpha/a.go" {
			t.Fatalf("symbol %s carries file %q, want alpha/a.go", s.Name, s.File)
		}
		if s.Kind != want[s.Name] {
			t.Fatalf("symbol %s carries kind %q, want %q", s.Name, s.Kind, want[s.Name])
		}
		if s.Line <= 0 {
			t.Fatalf("symbol %s carries line %d", s.Name, s.Line)
		}
		delete(want, s.Name)
	}
	if len(want) != 0 {
		t.Fatalf("symbols missing: %v", want)
	}
	wantEdges := []graph.Edge{{
		FromPackage: "example.com/m/alpha",
		FromName:    "Rec.Move",
		ToPackage:   "example.com/m/alpha",
		ToName:      "Rec",
		File:        "alpha/a.go",
		Line:        5,
	}}
	if len(res.Edges) != len(wantEdges) {
		t.Fatalf("edges = %v, want exactly %v", res.Edges, wantEdges)
	}
	for i, e := range res.Edges {
		if e != wantEdges[i] {
			t.Fatalf("edge %d = %v, want %v", i, e, wantEdges[i])
		}
	}
}

func TestGoExtractWalksTheUses(t *testing.T) {
	root := writeModule(t)
	res := extract(t, filepath.Join(root, "beta", "b.go"))
	if len(res.Symbols) != 1 || res.Symbols[0].Name != "Caller" || res.Symbols[0].Kind != "func" {
		t.Fatalf("symbols = %v, want Caller/func", res.Symbols)
	}
	want := map[string]int64{
		"example.com/m/alpha|Rec":      6,
		"example.com/m/alpha|Rec.Move": 7,
		"example.com/m/alpha|Alpha":    7,
		"example.com/m/alpha|Limit":    7,
		"example.com/m/alpha|Shared":   7,
	}
	for _, e := range res.Edges {
		if e.FromPackage != "example.com/m/beta" || e.FromName != "Caller" {
			t.Fatalf("edge owner = %v, want beta.Caller", e)
		}
		if e.File != "beta/b.go" {
			t.Fatalf("edge file = %q, want beta/b.go", e.File)
		}
		key := e.ToPackage + "|" + e.ToName
		line, ok := want[key]
		if !ok {
			t.Fatalf("unexpected edge %v", e)
		}
		if e.Line != line {
			t.Fatalf("edge to %s sits on line %d, want %d", key, e.Line, line)
		}
		delete(want, key)
	}
	for key := range want {
		t.Fatalf("edge to %s missing from %v", key, res.Edges)
	}
}

func TestTypeCheckFailureKeepsTheNames(t *testing.T) {
	root := writeModule(t)
	res := extract(t, filepath.Join(root, "gamma", "g.go"))
	names := map[string]string{}
	for _, s := range res.Symbols {
		names[s.Name] = s.Kind
	}
	if names["Here"] != "func" || names["also"] != "func" {
		t.Fatalf("names = %v, want Here and also as funcs", names)
	}
	var intra bool
	for _, e := range res.Edges {
		if e.ToPackage == "example.com/m/gamma" && e.ToName == "also" && e.FromName == "Here" {
			intra = true
		}
	}
	if !intra {
		t.Fatalf("the intra-package edge is missing: %v", res.Edges)
	}
}

func TestUnchangedShaIsNoop(t *testing.T) {
	root := writeModule(t)
	db := openTestStore(t)
	abs := filepath.Join(root, "alpha", "a.go")
	rel := "alpha/a.go"
	first, err := graph.Apply(context.Background(), db, rel, shaFile(t, abs), "go", extract(t, abs))
	if err != nil || !first {
		t.Fatalf("first apply: written=%v err=%v", first, err)
	}
	second, err := graph.Apply(context.Background(), db, rel, shaFile(t, abs), "go", extract(t, abs))
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if second {
		t.Fatal("the unchanged sha wrote")
	}
	var rows int
	if err := db.QueryRow("SELECT count(*) FROM symbols").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 5 {
		t.Fatalf("rows = %d, want 5", rows)
	}
}

func TestApplyReplacesSymbolsAndEdgesInPlace(t *testing.T) {
	root := writeModule(t)
	db := openTestStore(t)
	abs := filepath.Join(root, "alpha", "a.go")
	rel := "alpha/a.go"
	ctx := context.Background()
	x := filepath.Join(root, "alpha", "x.go")
	if _, err := graph.Apply(ctx, db, "alpha/x.go", shaFile(t, x), "go", extract(t, x)); err != nil {
		t.Fatalf("apply x.go: %v", err)
	}
	res := extract(t, abs)
	if _, err := graph.Apply(ctx, db, rel, shaFile(t, abs), "go", res); err != nil {
		t.Fatalf("apply: %v", err)
	}
	v2 := `package alpha

func New() int { return other() + Replaced() }

func Replaced() int { return 3 }
`
	if err := os.WriteFile(abs, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	res = extract(t, abs)
	if _, err := graph.Apply(ctx, db, rel, shaFile(t, abs), "go", res); err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	got := map[string]string{}
	rows, err := db.Query("SELECT name, file FROM symbols WHERE package = 'example.com/m/alpha'")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n, f string
		if err := rows.Scan(&n, &f); err != nil {
			t.Fatal(err)
		}
		got[n] = f
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	want := map[string]string{"New": "alpha/a.go", "Replaced": "alpha/a.go", "other": "alpha/x.go"}
	if len(got) != len(want) {
		t.Fatalf("symbols = %v, want exactly %v", got, want)
	}
	for n, f := range want {
		if got[n] != f {
			t.Fatalf("symbol %s sits in %q, want %q (all: %v)", n, got[n], f, got)
		}
	}
	var edges int
	if err := db.QueryRow("SELECT count(*) FROM edges WHERE from_name = 'Old'").Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if edges != 0 {
		t.Fatalf("the replaced file left Old's edges behind (%d)", edges)
	}
	if err := db.QueryRow("SELECT count(*) FROM edges WHERE from_name = 'New' AND to_name = 'other' AND to_package = 'example.com/m/alpha'").Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if edges != 1 {
		t.Fatalf("New's edge to other = %d, want 1", edges)
	}
}

func TestAMovingSymbolMovesItsRow(t *testing.T) {
	root := writeModule(t)
	db := openTestStore(t)
	ctx := context.Background()
	a := filepath.Join(root, "alpha", "a.go")
	if _, err := graph.Apply(ctx, db, "alpha/a.go", shaFile(t, a), "go", extract(t, a)); err != nil {
		t.Fatalf("apply a.go: %v", err)
	}
	v2 := `package alpha

func Shared() int { return 9 }
`
	if err := os.WriteFile(a, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Apply(ctx, db, "alpha/a.go", shaFile(t, a), "go", extract(t, a)); err != nil {
		t.Fatalf("apply a.go v2: %v", err)
	}
	var file string
	if err := db.QueryRow("SELECT file FROM symbols WHERE name = 'Shared' AND package = 'example.com/m/alpha'").Scan(&file); err != nil {
		t.Fatalf("the moved symbol is gone: %v", err)
	}
	if file != "alpha/a.go" {
		t.Fatalf("the moved symbol stayed in %q", file)
	}
}

func TestVendorAndTestdataAreSkipped(t *testing.T) {
	root := writeModule(t)
	for _, p := range []string{"vendor/v.go", "testdata/x.go", "alpha/testdata/y.go"} {
		if !graph.Skip(filepath.Join(root, p)) {
			t.Fatalf("%s is not skipped", p)
		}
	}
	if graph.Skip(filepath.Join(root, "alpha", "a.go")) {
		t.Fatal("alpha/a.go is skipped")
	}
	vendor := filepath.Join(root, "vendor", "v.go")
	if _, err := new(graph.GoExtract).Extract(context.Background(), vendor); err == nil {
		t.Fatal("the vendor extraction did not refuse")
	}
}

func TestProjectOfFindsTheModule(t *testing.T) {
	root := writeModule(t)
	rr, module, err := graph.ProjectOf(filepath.Join(root, "alpha", "a.go"))
	if err != nil {
		t.Fatalf("project of: %v", err)
	}
	if rr != root || module != "example.com/m" {
		t.Fatalf("project of alpha/a.go = (%s, %s)", rr, module)
	}
	sub := filepath.Join(root, "alpha")
	if pkg := graph.PackagePath(rr, module, sub); pkg != "example.com/m/alpha" {
		t.Fatalf("package path = %q", pkg)
	}
	nested := t.TempDir()
	if err := os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module other.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(nested, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	rr, module, err = graph.ProjectOf(filepath.Join(nested, "deep", "x.go"))
	if err != nil {
		t.Fatalf("project of: %v", err)
	}
	if rr != nested || module != "other.example" {
		t.Fatalf("project of nested = (%s, %s)", rr, module)
	}
	plain := t.TempDir()
	rr, module, err = graph.ProjectOf(filepath.Join(plain, "x.go"))
	if err != nil {
		t.Fatalf("project of: %v", err)
	}
	if rr != plain || module != "" {
		t.Fatalf("project of a moduleless dir = (%s, %s)", rr, module)
	}
}

func TestEdgesSurviveAnotherFilesExtraction(t *testing.T) {
	root := writeModule(t)
	db := openTestStore(t)
	ctx := context.Background()
	b := filepath.Join(root, "beta", "b.go")
	if _, err := graph.Apply(ctx, db, "beta/b.go", shaFile(t, b), "go", extract(t, b)); err != nil {
		t.Fatalf("apply b.go: %v", err)
	}
	a := filepath.Join(root, "alpha", "a.go")
	if _, err := graph.Apply(ctx, db, "alpha/a.go", shaFile(t, a), "go", extract(t, a)); err != nil {
		t.Fatalf("apply a.go: %v", err)
	}
	var edges int
	if err := db.QueryRow("SELECT count(*) FROM edges WHERE file = 'beta/b.go' AND to_name = 'Alpha'").Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if edges != 1 {
		t.Fatalf("a.go's extraction clobbered b.go's edge (count %d)", edges)
	}
}

func openTestStore(t *testing.T) store.DB {
	t.Helper()
	db, err := graph.Open(t.TempDir(), "testscope")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
