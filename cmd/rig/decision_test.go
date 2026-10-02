package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
)

func TestTheWiredGatesRecordIntoTheStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decision.sqlite")
	db, _, _, err := store.Open(path, decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	r := testRoot(nullFrontend{})
	r.drec = decisionstore.Recorder{DB: db, Scope: "proj"}
	k := wire(r)
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ran", nil
	}
	for _, mw := range k.Middleware {
		exec = mw.Wrap(exec)
	}
	out, err := exec(context.Background(), core.ToolCall{ID: "c1", Name: "no-such-tool", Args: nil})
	if err == nil {
		t.Fatalf("the denial must stand, got %q", out)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM decisions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("one row per gate decision, got %d", count)
	}
	var site, status string
	if err := db.QueryRow(`SELECT site, status FROM decisions`).Scan(&site, &status); err != nil {
		t.Fatal(err)
	}
	if site != decision.SitePerm || status != decision.StatusFinal {
		t.Fatalf("the perm denial is a final row: %q %q", site, status)
	}
}

func TestNoDecisionStoreMeansNoRecorder(t *testing.T) {
	r := testRoot(nullFrontend{})
	if r.drec != nil {
		t.Fatal("a root without a store records nothing")
	}
}
