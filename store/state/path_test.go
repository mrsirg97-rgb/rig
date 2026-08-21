package state_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state"
)

// TestStorePathRoundTrip (SPEC_SERVE 2, named): the path helper resolves to
// the same file the root opens — open via the helper, write a session, read
// it back. The root and the dashboard share one source; the formula is the
// first six sha1 bytes of the cwd, under <home>/sessions.
func TestStorePathRoundTrip(t *testing.T) {
	home := t.TempDir()
	cwd := "/workspace/roundtrip"
	path := state.StorePath(home, cwd)
	if filepath.Dir(path) != filepath.Join(home, "sessions") {
		t.Fatalf("path %q, want it under %s/sessions", path, home)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, _, err := store.Open(path, state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, "s1", cwd, "m", "v"); err != nil {
		t.Fatal(err)
	}
	rows, err := state.ListSessions(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Cwd != cwd {
		t.Fatalf("round-trip: %+v, want one session with cwd %s", rows, cwd)
	}
	// the formula is stable: the same (home, cwd) always names the same file.
	if again := state.StorePath(home, cwd); again != path {
		t.Fatalf("StorePath not stable: %q vs %q", again, path)
	}
}
