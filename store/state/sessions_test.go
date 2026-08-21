package state_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state"
)

func countSessions(t *testing.T, db store.DB) int {
	t.Helper()
	var n int
	if err := db.DB.QueryRow(`SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countMessages(t *testing.T, db store.DB, sid string) int {
	t.Helper()
	var n int
	if err := db.DB.QueryRow(`SELECT count(*) FROM messages WHERE session_id = $1`, sid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestListSessionsCountsTurnsExcludingSummary (SPEC_COMMANDS 5, named):
// turns = the session's user rows minus the [compaction] summary rows
// (transcript machinery, not prompts); newest first; an unclosed row
// renders as 'open'.
func TestListSessionsCountsTurnsExcludingSummary(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()

	// three rows, in started_at order; the newest (c) is still open.
	if e := state.RecordSession(ctx, db, "a", "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	if _, e := state.RecordMessage(ctx, db, "a", "user", "one", nil, nil); e != nil {
		t.Fatal(e)
	}
	if _, e := state.RecordMessage(ctx, db, "a", "user", "[compaction] the summary", nil, nil); e != nil {
		t.Fatal(e)
	}
	if _, e := state.RecordMessage(ctx, db, "a", "user", "two", nil, nil); e != nil {
		t.Fatal(e)
	}
	if e := state.CloseSession(ctx, db, "a", "ok"); e != nil {
		t.Fatal(e)
	}

	time.Sleep(1100 * time.Millisecond) // a distinct started_at (ms resolution)
	if e := state.RecordSession(ctx, db, "b", "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	if e := state.CloseSession(ctx, db, "b", "fault"); e != nil {
		t.Fatal(e)
	}

	time.Sleep(1100 * time.Millisecond)
	if e := state.RecordSession(ctx, db, "c", "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	if _, e := state.RecordMessage(ctx, db, "c", "user", "live one", nil, nil); e != nil {
		t.Fatal(e)
	}

	rows, err := state.ListSessions(ctx, db)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	// newest first
	if rows[0].ID != "c" || rows[1].ID != "b" || rows[2].ID != "a" {
		t.Fatalf("newest first, got %v %v %v", rows[0].ID, rows[1].ID, rows[2].ID)
	}
	// the turns count: a has 3 user-role rows minus the 1 summary row = 2
	if rows[2].Turns != 2 {
		t.Fatalf("turns = %d, want 2 (user rows minus [compaction] rows)", rows[2].Turns)
	}
	if rows[2].Exit != "ok" {
		t.Fatalf("closed row exit = %q, want ok", rows[2].Exit)
	}
	if rows[1].Exit != "fault" {
		t.Fatalf("fault row exit = %q, want fault", rows[1].Exit)
	}
	if rows[0].Exit != "open" {
		t.Fatalf("the unclosed row must render as open, got %q", rows[0].Exit)
	}
}

// TestListSessionsCapped (SPEC_COMMANDS 5): the list is capped at 50 —
// a glance, not an archive.
func TestListSessionsCapped(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	for i := 0; i < 55; i++ {
		if e := state.RecordSession(ctx, db, string(rune('a'+i%26))+string(rune('0'+i/26))+string(rune('a'+(i*7)%26)), "/w", "m", "v"); e != nil {
			t.Fatal(e)
		}
		time.Sleep(time.Millisecond)
	}
	rows, err := state.ListSessions(ctx, db)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != 50 {
		t.Fatalf("the list must be capped at 50, got %d", len(rows))
	}
}

// TestResumeNoSuchSessionIsTyped (SPEC_COMMANDS 5): the unknown-id
// refusal carries a sentinel the sessions command builds its voice on —
// and the text stays the existing resume voice.
func TestResumeNoSuchSessionIsTyped(t *testing.T) {
	db := openStore(t)
	_, err := state.Resume(context.Background(), db, "nope")
	if err == nil {
		t.Fatal("an unknown id must refuse")
	}
	if !errors.Is(err, state.ErrNoSuchSession) {
		t.Fatalf("the refusal must carry the sentinel for the command's voice: %v", err)
	}
	if err.Error() != "resume: no such session: nope" {
		t.Fatalf("the resume voice must stay as it is: %q", err.Error())
	}
}

// TestRecorderEnsureIsIdempotent (SPEC_COMMANDS 4, handoff step 2):
// Ensure lands the session row before any row lands under the id — and
// is idempotent: a pre-existing row is adopted, never re-inserted.
func TestRecorderEnsureIsIdempotent(t *testing.T) {
	db := openStore(t)
	inner := &nullFrontend{}

	rec := state.NewRecorder(inner, db, "/w", "m", "v", "fresh", core.NewSession())
	if e := rec.Ensure(); e != nil {
		t.Fatalf("Ensure: %v", e)
	}
	if e := rec.Ensure(); e != nil {
		t.Fatalf("a second Ensure must be a no-op: %v", e)
	}
	// the row exists exactly once
	rows := countSessions(t, db)
	if rows != 1 {
		t.Fatalf("Ensure must land the row once (idempotent), got %d rows", rows)
	}

	// adoption: a pre-existing row is never re-inserted
	rec2 := state.NewRecorder(inner, db, "/w", "m", "v", "fresh", core.NewSession())
	if e := rec2.Ensure(); e != nil {
		t.Fatalf("Ensure over an existing row (adoption): %v", e)
	}
	if rows := countSessions(t, db); rows != 1 {
		t.Fatalf("adoption must not re-insert the row, got %d rows", rows)
	}
}

// TestRecorderRetargetLandsUnderTheNewId (SPEC_COMMANDS 4, handoff
// step 3): the retiring recorder is re-pointed before it completes —
// its in-flight Input lands the user row (and the files snapshot) under
// the new session's id.
func TestRecorderRetargetLandsUnderTheNewId(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	inner := &promptFrontend{line: "hello"}

	s1 := core.NewSession()
	s1.Append(core.Message{Role: core.RoleUser, Content: "earlier"})
	rec1 := state.NewRecorder(inner, db, "/w", "m", "v", "s1", s1)
	if e := rec1.Ensure(); e != nil {
		t.Fatal(e)
	}
	if _, e := state.RecordMessage(ctx, db, "s1", "user", "earlier", nil, nil); e != nil {
		t.Fatal(e)
	}

	// the handoff: close the old row, adopt the new session, re-target.
	if e := rec1.Close("ok"); e != nil {
		t.Fatal(e)
	}
	s2 := core.NewSession()
	rec2 := state.NewRecorder(&nullFrontend{}, db, "/w", "m", "v", s2.ID, s2)
	if e := rec2.Ensure(); e != nil {
		t.Fatal(e)
	}
	rec1.Retarget(s2.ID, s2)

	// the in-flight Input (the handoff's dispatch) lands under s2.
	if text, err := rec1.Input(ctx); err != nil || text != "hello" {
		t.Fatalf("in-flight input: %q %v", text, err)
	}
	under := map[string]int{}
	under["s1"] = countMessages(t, db, "s1")
	under[s2.ID] = countMessages(t, db, s2.ID)
	if under["s1"] != 1 {
		t.Fatalf("the old row keeps its earlier rows only, got %d", under["s1"])
	}
	if under[s2.ID] != 1 {
		t.Fatalf("the in-flight user row must land under the new id, got %d", under[s2.ID])
	}
}

// promptFrontend serves one line, then EOFs.
type promptFrontend struct{ line string }

func (f *promptFrontend) Input(context.Context) (string, error) {
	if f.line == "" {
		return "", io.EOF
	}
	l := f.line
	f.line = ""
	return l, nil
}
func (*promptFrontend) Notify(core.Event) {}

type nullFrontend struct{}

func (*nullFrontend) Input(context.Context) (string, error) { return "", io.EOF }
func (*nullFrontend) Notify(core.Event)                     {}

// NewestSince is the delegate's session lookup (SPEC_DELEGATE 3): the
// newest session in a cwd started at or after a moment; none is the
// named refusal. The heuristic's race (another session in the same cwd
// during the window) is the spec's, not this verb's.
func TestNewestSinceNamesTheLatestSessionInCwd(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	before := time.Now().Add(-time.Hour)
	if e := state.RecordSession(ctx, db, "old", "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	time.Sleep(1100 * time.Millisecond)
	mid := time.Now()
	time.Sleep(1100 * time.Millisecond)
	if e := state.RecordSession(ctx, db, "new", "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	if e := state.RecordSession(ctx, db, "elsewhere", "/other", "m", "v"); e != nil {
		t.Fatal(e)
	}
	id, err := state.NewestSince(ctx, db, "/w", mid)
	if err != nil || id != "new" {
		t.Fatalf("since mid: (%q, %v), want new", id, err)
	}
	id, err = state.NewestSince(ctx, db, "/w", before)
	if err != nil || id != "new" {
		t.Fatalf("since before: (%q, %v), want the newest (new)", id, err)
	}
	if _, err := state.NewestSince(ctx, db, "/w", time.Now().Add(time.Hour)); !errors.Is(err, state.ErrNoSuchSession) {
		t.Fatalf("since the future: %v, want ErrNoSuchSession", err)
	}
	if _, err := state.NewestSince(ctx, db, "/nope", before); !errors.Is(err, state.ErrNoSuchSession) {
		t.Fatalf("unknown cwd: %v, want ErrNoSuchSession", err)
	}
}
