package state_test

import (
	"context"
	"errors"
	"fmt"
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

func TestListSessionsCountsTurnsExcludingSummary(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()

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

	time.Sleep(1100 * time.Millisecond)
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

	if _, e := state.RecordFault(ctx, db, "b", time.Now(), "provider: the stream died\nsecond line"); e != nil {
		t.Fatal(e)
	}

	rows, err := state.ListSessions(ctx, db, 50)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}

	if rows[0].ID != "c" || rows[1].ID != "b" || rows[2].ID != "a" {
		t.Fatalf("newest first, got %v %v %v", rows[0].ID, rows[1].ID, rows[2].ID)
	}

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
	if rows[1].Model != "m" || rows[1].Version != "v" {
		t.Fatalf("the row must carry the session's model and version, got %q %q", rows[1].Model, rows[1].Version)
	}
	if rows[1].Faults != 1 {
		t.Fatalf("faults = %d, want 1 (the recorded fault)", rows[1].Faults)
	}
	if rows[0].Faults != 0 || rows[2].Faults != 0 {
		t.Fatalf("faultless sessions must count 0, got %d %d", rows[0].Faults, rows[2].Faults)
	}
}

func TestListSessionsHonorsN(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if e := state.RecordSession(ctx, db, fmt.Sprintf("s%02d", i), "/w", "m", "v"); e != nil {
			t.Fatal(e)
		}
		time.Sleep(time.Millisecond)
	}
	rows, err := state.ListSessions(ctx, db, 3)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("n must cap the list, got %d rows, want 3", len(rows))
	}
	if rows[0].ID != "s09" || rows[1].ID != "s08" || rows[2].ID != "s07" {
		t.Fatalf("the cap keeps the newest first, got %v %v %v", rows[0].ID, rows[1].ID, rows[2].ID)
	}
}

func TestListSessionsCapped(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	for i := 0; i < 55; i++ {
		if e := state.RecordSession(ctx, db, string(rune('a'+i%26))+string(rune('0'+i/26))+string(rune('a'+(i*7)%26)), "/w", "m", "v"); e != nil {
			t.Fatal(e)
		}
		time.Sleep(time.Millisecond)
	}
	rows, err := state.ListSessions(ctx, db, 50)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != 50 {
		t.Fatalf("the list must be capped at 50, got %d", len(rows))
	}
}

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

	rows := countSessions(t, db)
	if rows != 1 {
		t.Fatalf("Ensure must land the row once (idempotent), got %d rows", rows)
	}

	rec2 := state.NewRecorder(inner, db, "/w", "m", "v", "fresh", core.NewSession())
	if e := rec2.Ensure(); e != nil {
		t.Fatalf("Ensure over an existing row (adoption): %v", e)
	}
	if rows := countSessions(t, db); rows != 1 {
		t.Fatalf("adoption must not re-insert the row, got %d rows", rows)
	}
}

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

	if e := rec1.Close("ok"); e != nil {
		t.Fatal(e)
	}
	s2 := core.NewSession()
	rec2 := state.NewRecorder(&nullFrontend{}, db, "/w", "m", "v", s2.ID, s2)
	if e := rec2.Ensure(); e != nil {
		t.Fatal(e)
	}
	rec1.Retarget(s2.ID, s2)

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
