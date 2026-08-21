package rem_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/store"
	remstore "github.com/mrsirg97-rgb/rig/store/rem"
)

// TestFilePathRoundTrip (SPEC_SERVE 2, named): the rem path helper resolves
// to the same file the root opens — open via the helper, learn a memory,
// read it back. The root and the dashboard share one source.
func TestFilePathRoundTrip(t *testing.T) {
	home := t.TempDir()
	cwd := "/workspace/roundtrip"
	path := remstore.FilePath(home)
	if path != filepath.Join(home, "rem", "rem.sqlite") {
		t.Fatalf("path %q, want %s/rem/rem.sqlite", path, home)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, _, err := store.Open(path, remstore.Statements(), remstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	ctx := context.Background()
	if _, _, _, err := remstore.Learn(ctx, db, cwd, remstore.LearnInput{Content: "a fact"}); err != nil {
		t.Fatal(err)
	}
	rows, err := remstore.Recent(ctx, db, cwd, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !strings.Contains(rows[0], "a fact") {
		t.Fatalf("round-trip recent %v, want the learned memory", rows)
	}
}
