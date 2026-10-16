package core_test

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/core"
)

func TestSessionThreadingRoundTrips(t *testing.T) {
	ctx := context.Background()
	if _, ok := core.SessionFrom(ctx); ok {
		t.Fatal("bare ctx must carry no session")
	}
	s := core.NewSession()
	ctx = core.WithSession(ctx, s)
	back, ok := core.SessionFrom(ctx)
	if !ok || back != s {
		t.Fatal("session must thread into ctx and back, by identity")
	}
}

func TestSessionIDMintedUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := core.NewSession().ID
		if id == "" {
			t.Fatal("empty session id minted")
		}
		if seen[id] {
			t.Fatalf("duplicate session id: %q", id)
		}
		seen[id] = true
	}
}
