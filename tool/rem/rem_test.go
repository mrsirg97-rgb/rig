package rem_test

import (
	"context"
	"encoding/json"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/paths"
	"github.com/mrsirg97-rgb/rig/store"
	remdd "github.com/mrsirg97-rgb/rig/store/rem/ddl"
	remmeta "github.com/mrsirg97-rgb/rig/store/rem/metadata"
	"github.com/mrsirg97-rgb/rig/store/scope"
	remapi "github.com/mrsirg97-rgb/rig/tool/rem"
)

func newDB(t *testing.T) store.DB {
	t.Helper()
	statements := remdd.Statements()
	statements = append(statements, remmeta.ExtraStatements()...)
	statements = append(statements, remmeta.FtsStatements()...)
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "rem.sqlite"), statements, 1)
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
	for _, want := range []string{"learn commits a fact", "prune removes, reduces, or consolidates", "ids (mN)", "name project when the fact belongs to a repo you did not start in"} {
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
	if _, ok := schema.Props["project"]; !ok {
		t.Fatal("the schema must carry a project field (the deliberate project)")
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := osexec.Command("git", "-C", dir, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	cmd = osexec.Command("git", "-C", dir, "-c", "user.email=test@rig", "-c", "user.name=rig", "add", "-A")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v (%s)", err, out)
	}
	cmd = osexec.Command("git", "-C", dir, "-c", "user.email=test@rig", "-c", "user.name=rig", "commit", "-q", "-m", "seed")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v (%s)", err, out)
	}
}

func TestLearnWithProjectFromNonRepoCwdRecallsFromRepoAndWorktree(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)
	wt := filepath.Join(repo, "wt")
	if out, err := osexec.Command("git", "-C", repo, "worktree", "add", "-q", wt).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v (%s)", err, out)
	}
	t.Chdir(t.TempDir())
	db := newDB(t)
	tool := remapi.New(db)
	if _, err := exec(t, tool, context.Background(), map[string]any{
		"action": "learn", "content": "a fact about the repo", "project": repo,
	}); err != nil {
		t.Fatalf("learn with project: %v", err)
	}
	for _, proj := range []string{repo, wt} {
		reply, err := exec(t, tool, context.Background(), map[string]any{
			"action": "recall", "query": "fact about the repo", "project": proj,
		})
		if err != nil {
			t.Fatalf("recall %s: %v", proj, err)
		}
		if !strings.Contains(reply, "a fact about the repo") {
			t.Fatalf("a project learned from a non-repo cwd must recall from %s:\n%s", proj, reply)
		}
		if !strings.Contains(reply, filepath.Base(repo)) {
			t.Fatalf("the recall from %s must carry the repo's label:\n%s", proj, reply)
		}
	}
}

func TestRecallWithProjectFillsFromGlobalAfter(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)
	t.Chdir(t.TempDir())
	db := newDB(t)
	tool := remapi.New(db)
	if _, err := exec(t, tool, context.Background(), map[string]any{
		"action": "learn", "content": "widget local lore", "project": repo,
	}); err != nil {
		t.Fatalf("learn project: %v", err)
	}
	if _, err := exec(t, tool, context.Background(), map[string]any{
		"action": "learn", "content": "widget global lore", "scope": "global",
	}); err != nil {
		t.Fatalf("learn global: %v", err)
	}
	reply, err := exec(t, tool, context.Background(), map[string]any{
		"action": "recall", "query": "widget", "project": repo,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	var memLines []string
	for _, line := range strings.Split(strings.TrimSuffix(reply, "\n"), "\n") {
		if strings.HasPrefix(line, "m") {
			memLines = append(memLines, line)
		}
	}
	if len(memLines) != 2 {
		t.Fatalf("project recall must fill from global after, got:\n%s", reply)
	}
	if !strings.Contains(memLines[0], filepath.Base(repo)) {
		t.Fatalf("the project row must lead with its label:\n%s", reply)
	}
	localAt := strings.Index(reply, "widget local lore")
	globalAt := strings.Index(reply, "widget global lore")
	if localAt < 0 || globalAt < 0 || localAt > globalAt {
		t.Fatalf("the project row must lead, the global fill after:\n%s", reply)
	}
}

func TestProjectPlusGlobalRefuses(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)
	db := newDB(t)
	tool := remapi.New(db)
	for _, action := range []map[string]any{
		{"action": "learn", "content": "x", "project": repo, "scope": "global"},
		{"action": "reflect", "content": "x", "project": repo, "scope": "global"},
		{"action": "recall", "query": "x", "project": repo, "scope": "global"},
		{"action": "prune", "verb": "consolidate", "project": repo, "scope": "global"},
	} {
		if _, err := exec(t, tool, context.Background(), action); err == nil {
			t.Fatalf("project + global succeeded: %v", action)
		} else if !strings.Contains(err.Error(), "project") || !strings.Contains(err.Error(), "global") {
			t.Errorf("voice:\n%q", err.Error())
		}
	}
}

func TestRelativeAndTildeProjectResolveIdentically(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo)
	t.Setenv("HOME", home)
	db := newDB(t)
	tool := remapi.New(db)
	mwExec := paths.Middleware().Wrap(func(ctx context.Context, call core.ToolCall) (string, error) {
		return tool.Exec(ctx, call.Args)
	})
	tilearn, err := mwExec(context.Background(), core.ToolCall{
		Name: "rem", Args: json.RawMessage(`{"action":"learn","content":"tilde fact","project":"~/repo"}`),
	})
	if err != nil {
		t.Fatalf("tilde learn: %v", err)
	}
	abslearn, err := mwExec(context.Background(), core.ToolCall{
		Name: "rem", Args: json.RawMessage(`{"action":"learn","content":"tilde fact","project":"` + repo + `"}`),
	})
	if err != nil {
		t.Fatalf("abs learn: %v", err)
	}
	if !strings.Contains(tilearn, "learned m") {
		t.Fatalf("the tilde learn must land:\n%s", tilearn)
	}
	if !strings.Contains(abslearn, "already known") {
		t.Fatalf("a ~ path and its absolute twin must be one scope (the second learn dedups):\n%s", abslearn)
	}
	if scope.Key(repo) != scope.Key(filepath.Join(home, "repo")) {
		t.Fatalf("the two paths must share one scope")
	}
}
