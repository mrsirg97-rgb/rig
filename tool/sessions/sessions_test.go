package sessions_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state"
	sessions "github.com/mrsirg97-rgb/rig/tool/sessions"
)

type usageRow struct{ prompt, cacheRead int64 }
type faultRow struct {
	at  time.Time
	msg string
}

type seedSpec struct {
	id      string
	model   string
	version string
	turns   int
	usage   []usageRow
	faults  []faultRow
}

func seedSession(t *testing.T, db store.DB, s seedSpec) {
	t.Helper()
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, s.id, "/w", s.model, s.version); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < s.turns; i++ {
		if _, err := state.RecordMessage(ctx, db, s.id, "user", "prompt", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range s.usage {
		seq, err := state.RecordMessage(ctx, db, s.id, "assistant", "reply", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.RecordUsage(ctx, db, seq, u.prompt, 0, u.cacheRead, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range s.faults {
		if _, err := state.RecordFault(ctx, db, s.id, f.at, f.msg); err != nil {
			t.Fatal(err)
		}
	}
}

func openProject(t *testing.T, home, cwd string, specs ...seedSpec) {
	t.Helper()
	path := state.StorePath(home, cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, _, _, err := store.Open(path, state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	for _, s := range specs {
		seedSession(t, db, s)
		time.Sleep(time.Millisecond)
	}
}

func run(t *testing.T, tool core.Tool, args string) (string, error) {
	t.Helper()
	return tool.Exec(context.Background(), json.RawMessage(args))
}

func TestSessionsListThisProjectNewestFirst(t *testing.T) {
	home := t.TempDir()
	this := "/workspace/this"
	base := time.Now().Add(-time.Hour)
	openProject(t, home, this,
		seedSpec{id: "t0000001", model: "qwen3.8-workers", version: "0.15.0", turns: 1},
		seedSpec{id: "t0000002", model: "local", version: "0.16.1", turns: 5,
			faults: []faultRow{{at: base, msg: "provider: the stream died"}}},
		seedSpec{id: "t0000003", model: "local", version: "0.16.1", turns: 2},
	)
	tool := sessions.New(home, this)
	out, err := run(t, tool, `{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "t0000003") || !strings.Contains(lines[0], "model local") ||
		!strings.Contains(lines[0], "version 0.16.1") || !strings.Contains(lines[0], "turns 2") ||
		!strings.Contains(lines[0], "faults 0") {
		t.Fatalf("newest line wrong:\n%s", lines[0])
	}
	if !strings.HasPrefix(lines[1], "t0000002") || !strings.Contains(lines[1], "turns 5") ||
		!strings.Contains(lines[1], "faults 1") {
		t.Fatalf("middle line wrong:\n%s", lines[1])
	}
	if !strings.HasPrefix(lines[2], "t0000001") || !strings.Contains(lines[2], "model qwen3.8-workers") {
		t.Fatalf("oldest line wrong:\n%s", lines[2])
	}
	for _, l := range lines {
		if strings.Contains(l, "o0000001") {
			t.Fatalf("another project's session leaked into the list:\n%s", out)
		}
	}
}

func TestSessionsListHonorsProjectAndN(t *testing.T) {
	home := t.TempDir()
	this := "/workspace/this"
	other := "/workspace/other"
	openProject(t, home, this,
		seedSpec{id: "t0000001", model: "local", version: "0.16.1", turns: 1},
		seedSpec{id: "t0000002", model: "local", version: "0.16.1", turns: 1},
	)
	openProject(t, home, other,
		seedSpec{id: "o0000001", model: "other", version: "9.9.9", turns: 7},
	)
	tool := sessions.New(home, this)

	out, err := run(t, tool, `{"action":"list","project":"`+other+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "o0000001") || !strings.Contains(out, "model other") ||
		strings.Contains(out, "t0000001") {
		t.Fatalf("project must read that project's store:\n%s", out)
	}

	out, err = run(t, tool, `{"action":"list","n":1}`)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "t0000002") {
		t.Fatalf("n must cap the list to the newest, got:\n%s", out)
	}

	if _, err := run(t, tool, `{"action":"list","n":0}`); err == nil ||
		!strings.Contains(err.Error(), "n must be within 1..50") {
		t.Fatalf("an out-of-range n must refuse, got %v", err)
	}
	if _, err := run(t, tool, `{"action":"list","n":51}`); err == nil ||
		!strings.Contains(err.Error(), "n must be within 1..50") {
		t.Fatalf("an out-of-range n must refuse, got %v", err)
	}
}

func TestSessionsSummaryCacheRatioFixture(t *testing.T) {
	home := t.TempDir()
	cwd := "/workspace/vitals"
	base := time.Now().Add(-time.Hour)
	openProject(t, home, cwd,
		seedSpec{id: "a0000001", model: "local", version: "0.16.1", turns: 1,
			usage: []usageRow{{prompt: 100, cacheRead: 40}, {prompt: 100, cacheRead: 20}}},
		seedSpec{id: "b0000002", model: "qwen3.8-workers", version: "0.16.1", turns: 1,
			usage:  []usageRow{{prompt: 100, cacheRead: 80}},
			faults: []faultRow{{at: base, msg: "provider: the stream died\nsecond line"}}},
	)
	tool := sessions.New(home, cwd)
	out, err := run(t, tool, `{"action":"summary"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := "vitals: 2 sessions, 2 turns\n" +
		"models: local 0.16.1, qwen3.8-workers 0.16.1\n" +
		"faults: 1 — last: provider: the stream died\n" +
		"cache ratio: 46% (cache_read 140 / prompt 300)"
	if out != want {
		t.Fatalf("the summary must be exact:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

func TestSessionsSummaryNamesScopeWhenEmpty(t *testing.T) {
	home := t.TempDir()
	tool := sessions.New(home, "/workspace/emptyscope")
	out, err := run(t, tool, `{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "(no sessions in emptyscope)" {
		t.Fatalf("an empty store must name its scope (SPEC_CORE), got %q", out)
	}
	out, err = run(t, tool, `{"action":"summary"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "(no sessions in emptyscope)" {
		t.Fatalf("an empty store must name its scope (SPEC_CORE), got %q", out)
	}
}

func TestSessionsSummaryPicksTheLatestFaultAcrossSessions(t *testing.T) {
	home := t.TempDir()
	cwd := "/workspace/multifault"
	base := time.Now().Add(-time.Hour)
	openProject(t, home, cwd,
		seedSpec{id: "m0000001", model: "local", version: "0.16.1", turns: 1,
			faults: []faultRow{{at: base, msg: "old fault"}}},
		seedSpec{id: "m0000002", model: "local", version: "0.16.1", turns: 1,
			faults: []faultRow{{at: base.Add(time.Minute), msg: "new fault\nmore detail"}}},
	)
	tool := sessions.New(home, cwd)
	out, err := run(t, tool, `{"action":"summary"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "faults: 2 — last: new fault") {
		t.Fatalf("the last fault must be the latest across the slice:\n%s", out)
	}
	if strings.Contains(out, "old fault") {
		t.Fatalf("the older fault must not surface as the last:\n%s", out)
	}
}

func TestSessionsActionRefusals(t *testing.T) {
	home := t.TempDir()
	tool := sessions.New(home, "/workspace/this")
	if _, err := run(t, tool, `{}`); err == nil || err.Error() != "sessions: action required" {
		t.Fatalf("a missing action must refuse, got %v", err)
	}
	if _, err := run(t, tool, `{"action":"frob"}`); err == nil ||
		!strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("an unknown action must refuse, got %v", err)
	}
}

func TestSessionsShape(t *testing.T) {
	tool := sessions.New(t.TempDir(), "/workspace/this")
	if tool.Name() != "sessions" {
		t.Fatalf("name = %q", tool.Name())
	}
	desc := tool.Description()
	for _, part := range []string{"Guidelines:", "Reply:"} {
		if !strings.Contains(desc, part) {
			t.Fatalf("the description must carry the four-part shape, missing %s:\n%s", part, desc)
		}
	}
	schema := string(tool.Schema())
	for _, field := range []string{`"action"`, `"project"`, `"n"`} {
		if !strings.Contains(schema, field) {
			t.Fatalf("the schema must name %s:\n%s", field, schema)
		}
	}
}

func TestRelativeProjectResolvesToTheAbsoluteWorkspace(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	openProject(t, home, ws, seedSpec{id: "sess-rel", model: "m1", version: "0.16.1", turns: 1})
	sub := filepath.Join(ws, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(ws); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	tool := sessions.New(home, sub)
	out, err := tool.Exec(context.Background(), json.RawMessage(`{"action":"list","project":"."}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sess-rel") {
		t.Fatalf("a relative project must resolve to the absolute workspace:\n%s", out)
	}
}
