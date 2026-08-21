package rem_test

// The rem surface over store/rem at the adapter level — runtime shape
// refusals, the scope enum, source attribution (threaded session id,
// anon when unthreaded, free text when passed), reply pass-through.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	remdd "github.com/mrsirg97-rgb/rig/store/rem/ddl"
	remmeta "github.com/mrsirg97-rgb/rig/store/rem/metadata"
	remapi "github.com/mrsirg97-rgb/rig/tool/rem"
)

func newDB(t *testing.T) store.DB {
	t.Helper()
	statements := remdd.Statements()
	statements = append(statements, remmeta.ExtraStatements()...)
	statements = append(statements, remmeta.FtsStatements()...)
	db, _, err := store.Open(filepath.Join(t.TempDir(), "rem.sqlite"), statements, 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return db
}

func exec(t *testing.T, tool core.Tool, ctx context.Context, args map[string]any) (string, error) {
	t.Helper()
	payload, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Exec(ctx, payload)
}

func sourceOf(t *testing.T, db store.DB, content string) *string {
	t.Helper()
	var s *string
	err := db.QueryRow(`SELECT source FROM memories WHERE content = ?`, content).Scan(&s)
	if err != nil {
		return nil
	}
	return s
}

func TestBareRemIsLoudAtExecute(t *testing.T) {
	tool := remapi.New(newDB(t))
	if _, err := exec(t, tool, context.Background(), map[string]any{}); err == nil {
		t.Fatal("bare execute succeeded")
	} else if want := "rem: action required"; err.Error() != want {
		t.Errorf("bare voice:\n%q", err.Error())
	}
}

func TestUnknownActionRefusesLoudly(t *testing.T) {
	tool := remapi.New(newDB(t))
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "sync"}); err == nil {
		t.Fatal("unknown action succeeded")
	} else if want := "rem: action 'sync' not implemented"; err.Error() != want {
		t.Errorf("voice:\n%q", err.Error())
	}
}

func TestLearnMissingContentFailsLoudly(t *testing.T) {
	tool := remapi.New(newDB(t))
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "learn"}); err == nil {
		t.Fatal("learn without content succeeded")
	} else if want := "rem: action 'learn' requires content"; err.Error() != want {
		t.Errorf("voice:\n%q", err.Error())
	}
}

func TestReflectMissingContentFailsLoudly(t *testing.T) {
	tool := remapi.New(newDB(t))
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "reflect"}); err == nil {
		t.Fatal("reflect without content succeeded")
	} else if want := "rem: action 'reflect' requires content"; err.Error() != want {
		t.Errorf("voice:\n%q", err.Error())
	}
}

func TestImportanceShapeRefusesLoudly(t *testing.T) {
	tool := remapi.New(newDB(t))
	for _, bad := range []any{1.5, -0.1} {
		if _, err := exec(t, tool, context.Background(),
			map[string]any{"action": "learn", "content": "x", "importance": bad}); err == nil {
			t.Fatalf("importance %v succeeded", bad)
		} else if !strings.Contains(err.Error(), "importance must be within 0..1") {
			t.Errorf("voice:\n%q", err.Error())
		}
	}
}

func TestKShapeRefusesLoudly(t *testing.T) {
	tool := remapi.New(newDB(t))
	for _, bad := range []any{0, 51} {
		if _, err := exec(t, tool, context.Background(),
			map[string]any{"action": "recall", "query": "x", "k": bad}); err == nil {
			t.Fatalf("k %v succeeded", bad)
		} else if !strings.Contains(err.Error(), "k must be within 1..50") {
			t.Errorf("voice:\n%q", err.Error())
		}
	}
}

func TestScopeBogusRefusesLoudly(t *testing.T) {
	tool := remapi.New(newDB(t))
	for _, action := range []map[string]any{
		{"action": "recall", "query": "x", "scope": "bogus"},
		{"action": "learn", "content": "x", "scope": "bogus"},
		{"action": "reflect", "content": "x", "scope": "bogus"},
		{"action": "prune", "verb": "consolidate", "scope": "bogus"},
	} {
		if _, err := exec(t, tool, context.Background(), action); err == nil {
			t.Fatalf("bogus scope succeeded: %v", action)
		} else if want := "rem: scope must be project, global, or all, got 'bogus'"; err.Error() != want {
			t.Errorf("voice:\n%q", err.Error())
		}
	}
}

func TestScopeAllAtWriteRefusesLoudly(t *testing.T) {
	tool := remapi.New(newDB(t))
	if _, err := exec(t, tool, context.Background(),
		map[string]any{"action": "learn", "content": "x", "scope": "all"}); err == nil {
		t.Fatal("scope=all learn succeeded")
	} else if !strings.Contains(err.Error(), "scope must be project or global, got 'all'") {
		t.Errorf("voice:\n%q", err.Error())
	}
}

func TestSupersedesDecodeRefusesLoudly(t *testing.T) {
	tool := remapi.New(newDB(t))
	for _, bad := range []any{"notanid", 0.5, []any{1, "x"}} {
		if _, err := exec(t, tool, context.Background(),
			map[string]any{"action": "learn", "content": "x", "supersedes": bad}); err == nil {
			t.Fatalf("supersedes %v succeeded", bad)
		} else if !strings.Contains(err.Error(), "supersedes must be") {
			t.Errorf("voice:\n%q", err.Error())
		}
	}
}

func TestIdsDecodeRefusesLoudly(t *testing.T) {
	tool := remapi.New(newDB(t))
	if _, err := exec(t, tool, context.Background(),
		map[string]any{"action": "prune", "verb": "remove", "ids": []any{"x"}}); err == nil {
		t.Fatal("malformed ids succeeded")
	} else if !strings.Contains(err.Error(), "ids must be") {
		t.Errorf("voice:\n%q", err.Error())
	}
}

func TestPruneVerbVoices(t *testing.T) {
	tool := remapi.New(newDB(t))
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "prune"}); err == nil {
		t.Fatal("prune without verb succeeded")
	} else if !strings.Contains(err.Error(), "prune requires verb remove|reduce|consolidate") {
		t.Errorf("voice:\n%q", err.Error())
	}
	if _, err := exec(t, tool, context.Background(),
		map[string]any{"action": "prune", "verb": "sync"}); err == nil {
		t.Fatal("bogus verb succeeded")
	} else if !strings.Contains(err.Error(), "verb must be remove, reduce, or consolidate") {
		t.Errorf("voice:\n%q", err.Error())
	}
}

// memories.source defaults to the calling session id (anon when
// unthreaded), and accepts free text when the caller passes one.
func TestSourceAttributionThreadedSession(t *testing.T) {
	db := newDB(t)
	tool := remapi.New(db)
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	if _, err := exec(t, tool, ctx, map[string]any{"action": "learn", "content": "attributed"}); err != nil {
		t.Fatalf("learn: %v", err)
	}
	if s := sourceOf(t, db, "attributed"); s == nil || *s != sess.ID {
		t.Fatalf("source %v, want the threaded session id %s", s, sess.ID)
	}
}

func TestSourceAttributionAnonymousWhenUnthreaded(t *testing.T) {
	db := newDB(t)
	tool := remapi.New(db)
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "reflect", "content": "anon attributed"}); err != nil {
		t.Fatalf("reflect: %v", err)
	}
	if s := sourceOf(t, db, "anon attributed"); s == nil || *s != "anon" {
		t.Fatalf("source %v, want anon", s)
	}
}

func TestSourceExplicitRidesVerbatim(t *testing.T) {
	db := newDB(t)
	tool := remapi.New(db)
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "learn", "content": "explicit", "source": "free text source",
	}); err != nil {
		t.Fatalf("learn: %v", err)
	}
	if s := sourceOf(t, db, "explicit"); s == nil || *s != "free text source" {
		t.Fatalf("source %v, want the explicit free text", s)
	}
}

// Reply shaping passes through the store's voices unaltered.
func TestReplyVoicesPassThrough(t *testing.T) {
	db := newDB(t)
	tool := remapi.New(db)
	reply, err := exec(t, tool, context.Background(), map[string]any{
		"action": "learn", "content": "voice probe", "kind": "constraint", "importance": 0.8,
	})
	if err != nil {
		t.Fatalf("learn: %v", err)
	}
	if !strings.Contains(reply, "learned m") || !strings.Contains(reply, " · constraint · 0.8") {
		t.Fatalf("reply %q", reply)
	}
	reply2, err := exec(t, tool, context.Background(), map[string]any{
		"action": "learn", "content": "voice probe",
	})
	if err != nil {
		t.Fatalf("re-learn: %v", err)
	}
	if !strings.Contains(reply2, "already known m") {
		t.Fatalf("reply %q", reply2)
	}
	_, err = exec(t, tool, context.Background(), map[string]any{
		"action": "prune", "verb": "remove", "ids": []any{999999},
	})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	reply3, _ := exec(t, tool, context.Background(), map[string]any{
		"action": "prune", "verb": "remove", "ids": []any{999999},
	})
	if !strings.Contains(reply3, "removed 0") {
		t.Fatalf("reply %q", reply3)
	}
}

func TestNameDescriptionSchemaShape(t *testing.T) {
	tool := remapi.New(newDB(t))
	if tool.Name() != "rem" {
		t.Fatalf("name %q", tool.Name())
	}
	d := tool.Description()
	for _, want := range []string{"learn commits facts", "prune removes, reduces, or consolidates", "Ids are minted mN"} {
		if !strings.Contains(d, want) {
			t.Fatalf("description missing %q:\n%s", want, d)
		}
	}
	var schema struct {
		Type     string   `json:"type"`
		Required []string `json:"required"`
		Props    map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if schema.Type != "object" || len(schema.Required) != 1 || schema.Required[0] != "action" {
		t.Fatalf("schema %+v", schema)
	}
	if got := schema.Props["action"].Enum; len(got) != 4 || got[0] != "learn" || got[3] != "prune" {
		t.Fatalf("action enum %v", got)
	}
	if got := schema.Props["scope"].Enum; len(got) != 3 || got[2] != "all" {
		t.Fatalf("scope enum %v", got)
	}
	if got := schema.Props["verb"].Enum; len(got) != 3 || got[0] != "remove" {
		t.Fatalf("verb enum %v", got)
	}
}
