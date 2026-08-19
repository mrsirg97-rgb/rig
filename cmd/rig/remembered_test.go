package main

// SPEC_UX 2, named: the remembered notes ride the prefix at session
// start and the refresh points — the segment's order, its absence when
// there are no notes (today's bytes exactly), the K and character caps,
// the refresh points' re-read, and the cwd scoping.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/perm"
	"github.com/mrsirg97-rgb/rig/store"
	remstore "github.com/mrsirg97-rgb/rig/store/rem"
)

func openRemDB(t *testing.T) store.DB {
	t.Helper()
	db, _, err := store.Open(filepath.Join(t.TempDir(), "rem.sqlite"), remstore.Statements(), remstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB.Close() })
	return db
}

func learnNote(t *testing.T, db store.DB, cwd, content string) {
	t.Helper()
	if _, _, _, err := remstore.Learn(context.Background(), db, cwd, remstore.LearnInput{Content: content}); err != nil {
		t.Fatal(err)
	}
}

// TestRememberedSegmentRidesAfterAgentsBeforeGuidelines (SPEC_UX 2,
// named): the segment sits after AGENTS.md, before the guidelines —
// descending proximity, one order.
func TestRememberedSegmentRidesAfterAgentsBeforeGuidelines(t *testing.T) {
	gw := guidelineMW{
		ToolMiddlewareFunc: func(next core.ToolExec) core.ToolExec { return next },
		text:               "GUIDELINE-PROSE",
	}
	remDB := openRemDB(t)
	r := testRoot(nullFrontend{})
	r.agents = "G\n\nP"
	r.middleware = []core.ToolMiddleware{perm.Allowlist("bash"), gw}
	r.remDB = remDB
	r.cwd = "/ws/mem"
	learnNote(t, remDB, "/ws/mem", "the repo lives in ~/Projects/rig")
	learnNote(t, remDB, "/ws/mem", "push with GIT_SSH_COMMAND set")
	r.fullSystem = r.buildSystem()
	want := "be terse" + "\n\n" +
		"G\n\nP" + "\n\n" +
		"remembered (this directory):\n" +
		"- push with GIT_SSH_COMMAND set\n" +
		"- the repo lives in ~/Projects/rig" + "\n\n" +
		"GUIDELINE-PROSE"
	if r.fullSystem != want {
		t.Fatalf("fullSystem = %q, want %q (system, AGENTS.md, the remembered notes, guidelines)", r.fullSystem, want)
	}
}

// TestRememberedSegmentAbsentKeepsTodaysBytes (SPEC_UX 2, named): no
// notes, no segment — the 0.2.0 assembly stands exactly.
func TestRememberedSegmentAbsentKeepsTodaysBytes(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.agents = "G\n\nP"
	r.remDB = openRemDB(t)
	r.cwd = "/ws/empty"
	r.fullSystem = r.buildSystem()
	want := "be terse" + "\n\n" + "G\n\nP"
	if r.fullSystem != want {
		t.Fatalf("absent notes keep today's bytes exactly, got:\n%q", r.fullSystem)
	}
	// and a root with no rem store at all (the test roots' shape)
	r2 := testRoot(nullFrontend{})
	r2.fullSystem = r2.buildSystem()
	if r2.fullSystem != "be terse" {
		t.Fatalf("no store, no segment, got:\n%q", r2.fullSystem)
	}
}

// TestRememberedSegmentCaps (SPEC_UX 2, named): K = 8 newest notes
// only, and the 1500-character cap — over it the oldest trim first, and
// one note over the cap alone is cut with the loud ellipsis.
func TestRememberedSegmentCaps(t *testing.T) {
	remDB := openRemDB(t)
	for i := 1; i <= 10; i++ {
		learnNote(t, remDB, "/ws/caps", fmt.Sprintf("note %02d", i))
	}
	r := testRoot(nullFrontend{})
	r.remDB = remDB
	r.cwd = "/ws/caps"
	r.fullSystem = r.buildSystem()
	if !strings.Contains(r.fullSystem, "note 08") || !strings.Contains(r.fullSystem, "note 10") {
		t.Fatalf("the newest K ride:\n%q", r.fullSystem)
	}
	if strings.Contains(r.fullSystem, "note 01") || strings.Contains(r.fullSystem, "note 02") {
		t.Fatalf("the two oldest must not ride (K=8):\n%q", r.fullSystem)
	}

	// the character cap: one note long enough to overflow the segment
	// alone (learned last: the newest, so it leads the K)
	long := strings.Repeat("x", 1600)
	learnNote(t, remDB, "/ws/caps", long)
	r2 := testRoot(nullFrontend{})
	r2.remDB = remDB // the same store: 10 short + 1 long = 11 notes
	r2.cwd = "/ws/caps"
	r2.fullSystem = r2.buildSystem()
	seg := segmentOf(t, r2.fullSystem)
	if runes := len([]rune(seg)); runes > 1500 {
		t.Fatalf("the segment must stay within 1500 characters, got %d:\n%q", runes, seg)
	}
	if !strings.HasSuffix(strings.TrimSpace(seg), "…") {
		t.Fatalf("a cut note must end with the loud ellipsis:\n%q", seg)
	}
}

func segmentOf(t *testing.T, full string) string {
	t.Helper()
	_, after, ok := strings.Cut(full, "remembered (this directory):")
	if !ok {
		t.Fatal("no remembered segment")
	}
	seg := "remembered (this directory):" + after
	// the segment runs to the next blank-line join (the guidelines) or the end
	if i := strings.Index(seg, "\n\n"); i >= 0 {
		seg = seg[:i]
	}
	return seg
}

// TestRememberedSegmentRefreshesAtTheRefreshPoints (SPEC_UX 2, named):
// the read is at session start and the refresh points (new, resume) —
// a note learned after the start rides the next session's prefix.
func TestRememberedSegmentRefreshesAtTheRefreshPoints(t *testing.T) {
	h := newHarness(t, defaultRow(), "local", defaultsTable(t))
	if strings.Contains(h.r.fullSystem, "remembered (this directory):") {
		t.Fatal("the fresh session's prefix must not carry the segment yet")
	}
	learnNote(t, h.remDB, h.r.cwd, "the late note")
	if _, err := h.r.newSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.r.fullSystem, "- the late note") {
		t.Fatalf("the refresh point must re-read the cwd's notes, got:\n%q", h.r.fullSystem)
	}
}

// TestRememberedSegmentIsCwdScoped (SPEC_UX 2, named): another
// directory's notes never ride — the cwd is the scope rem already chose.
func TestRememberedSegmentIsCwdScoped(t *testing.T) {
	remDB := openRemDB(t)
	learnNote(t, remDB, "/ws/home", "a note in another directory")
	r := testRoot(nullFrontend{})
	r.remDB = remDB
	r.cwd = "/ws/other"
	r.fullSystem = r.buildSystem()
	if strings.Contains(r.fullSystem, "remembered (this directory):") {
		t.Fatalf("another directory's notes must not ride, got:\n%q", r.fullSystem)
	}
}
