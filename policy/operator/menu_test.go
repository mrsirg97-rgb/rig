package operator

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/plugins"
	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
	"github.com/mrsirg97-rgb/rig/v2/tool/bash"
	schedapi "github.com/mrsirg97-rgb/rig/v2/tool/scheduler"
	todoapi "github.com/mrsirg97-rgb/rig/v2/tool/todo"
)

type fakeCrontab struct{}

func (fakeCrontab) List() (string, error)     { return "", nil }
func (fakeCrontab) Install(text string) error { return nil }

type fakeLive struct{}

func (fakeLive) PluginNames() []string                { return []string{"syshealth"} }
func (fakeLive) Plugin(name string) (core.Tool, bool) { return fakePlugin{}, true }

type fakePlugin struct{}

func (fakePlugin) Name() string { return "syshealth" }
func (fakePlugin) Description() string {
	return "what: a live plugin. guidelines: call it. reply: its text."
}
func (fakePlugin) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"n":{"type":"string"}}}`)
}
func (fakePlugin) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "ran", nil
}

func openStore(t *testing.T, dir, file string, statements []string, version int) store.DB {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(dir, file), statements, version)
	if err != nil {
		t.Fatalf("open %s: %v", file, err)
	}
	t.Cleanup(func() { db.DB.Close() })
	return db
}

func todoFixture(t *testing.T) core.Tool {
	t.Helper()
	db := openStore(t, t.TempDir(), "todo.sqlite", todostore.Statements(), todostore.SchemaVersion)
	return todoapi.New(db, todoapi.Worker)
}

func schedulerFixture(t *testing.T) core.Tool {
	t.Helper()
	home := t.TempDir()
	db := openStore(t, home, "global.sqlite", sched.Statements(), sched.SchemaVersion)
	return schedapi.New(db, fakeCrontab{}, "rig run-job", "local", home)
}

func pluginFixture() core.Tool {
	return plugins.NewDoor(fakeLive{}, nil, nil)
}

func actionEnum(t *testing.T, spec core.Tool) []string {
	t.Helper()
	var schema struct {
		Properties struct {
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(spec.Schema(), &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return schema.Properties.Action.Enum
}

func enumHas(t *testing.T, enum []string, want, gone []string) {
	t.Helper()
	set := map[string]bool{}
	for _, v := range enum {
		set[v] = true
	}
	for _, v := range want {
		if !set[v] {
			t.Fatalf("the headless menu lost %q: %v", v, enum)
		}
	}
	for _, v := range gone {
		if set[v] {
			t.Fatalf("the headless menu still offers %q: %v", v, enum)
		}
	}
}

func TestTheHeadlessMenuDropsTheOperatorVerbsFromTheEnum(t *testing.T) {
	enum := actionEnum(t, Menu(todoFixture(t)))
	enumHas(t, enum,
		[]string{"create", "claim", "start", "complete", "fail", "release", "retry", "read", "note", "notes", "finished"},
		[]string{"prune", "accept", "reject", "move"})
	if len(enum) != 11 {
		t.Fatalf("the todo enum kept %d verbs, want 11", len(enum))
	}
	enumHas(t, actionEnum(t, Menu(schedulerFixture(t))),
		[]string{"create", "update", "list", "show", "pause", "resume", "runs", "repair"},
		[]string{"remove"})
	enumHas(t, actionEnum(t, Menu(pluginFixture())),
		[]string{"run", "schema", "list", "create", "reload"},
		[]string{"delete"})
}

func TestTheHeadlessMenuDoesNotSayTheOperatorVerbs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spec  core.Tool
		verbs []string
	}{
		{"todo", todoFixture(t), []string{"prune", "accept", "reject", "move"}},
		{"scheduler", schedulerFixture(t), []string{"remove"}},
		{"plugin", pluginFixture(), []string{"delete"}},
	} {
		wrapped := Menu(tc.spec)
		for _, verb := range tc.verbs {
			re := regexp.MustCompile(`\b` + verb + `\b`)
			if re.MatchString(wrapped.Description()) {
				t.Fatalf("%s: the headless description still says %q: %s", tc.name, verb, wrapped.Description())
			}
			if re.Match(wrapped.Schema()) {
				t.Fatalf("%s: the headless schema still says %q: %s", tc.name, verb, wrapped.Schema())
			}
		}
	}
	if !strings.Contains(string(pluginFixture().Schema()), "delete") {
		t.Fatal("the registry's own schema must still say delete for the door (the interactive wire is unchanged)")
	}
	if !strings.Contains(string(schedulerFixture(t).Schema()), "pause/resume/remove/runs/show") {
		t.Fatal("the registry's own schema must keep the id verbs intact (the interactive wire is unchanged)")
	}
}

func TestTheHeadlessMenuKeepsTheWorkersOwnVerbsReadable(t *testing.T) {
	wrapped := Menu(schedulerFixture(t))
	if !strings.Contains(string(wrapped.Schema()), "pause/resume/runs/show") {
		t.Fatalf("the id description must keep the verbs a worker uses: %s", wrapped.Schema())
	}
	if !strings.Contains(wrapped.Description(), "persistent background jobs") {
		t.Fatalf("the description must survive the trim: %s", wrapped.Description())
	}
	todo := Menu(todoFixture(t))
	if !strings.Contains(string(todo.Schema()), `"status"`) {
		t.Fatalf("the untouched schema properties must survive: %s", todo.Schema())
	}
	if todo.Description() != todoFixture(t).Description() {
		t.Fatal("a tool whose description names no operator verb keeps it byte for byte")
	}
}

func TestTheHeadlessMenuKeepsTheLiveNameEnumAndTheExec(t *testing.T) {
	wrapped := Menu(pluginFixture())
	if !strings.Contains(string(wrapped.Schema()), "syshealth") {
		t.Fatalf("the door's live name enum must survive the trim: %s", wrapped.Schema())
	}
	todo := Menu(todoFixture(t))
	raw, err := json.Marshal(map[string]any{"action": "create", "text": "board entry", "scope": t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := todo.Exec(context.Background(), raw); err != nil {
		t.Fatalf("a trimmed tool still executes: %v", err)
	}
}

func TestTheMenuLeavesAToolWithoutOperatorVerbsAlone(t *testing.T) {
	spec := bash.New()
	if Menu(spec) != spec {
		t.Fatal("a tool the registry marks no operator verbs for is handed back, not wrapped")
	}
}
