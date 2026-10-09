package delegate_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v2/tool/delegate"
)

func doingTool(t *testing.T, h *harness, spawn *fakeSpawn, allow []string) delegate.Delegate {
	t.Helper()
	return delegate.New(delegate.Opts{
		Ctx:          context.Background(),
		Await:        true,
		DB:           h.db,
		Home:         h.home,
		RigHome:      h.rigHome,
		StateDir:     filepath.Join(h.rigHome, "sessions"),
		SwapURL:      "http://127.0.0.1:8090",
		WorkerCmd:    []string{"/x/rig"},
		DefaultModel: "qwen3.8-workers",
		Sandbox:      "off",
		Fetch:        fakeFetch(""),
		Spawn:        spawn.spawn,
		Models:       modelTable(t),
		Allow:        allow,
	})
}

func allowArg(t *testing.T, argv []string) string {
	t.Helper()
	for i, a := range argv {
		if a == "-allow" {
			return argv[i+1]
		}
	}
	return ""
}

func runDoing(t *testing.T, tool delegate.Delegate) {
	t.Helper()
	if _, err := tool.Run(context.Background(), "sweep the parsers", "", ""); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestADelegateSpawnCarriesTheDoingSet(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	tool := doingTool(t, h, spawn, []string{
		"bash", "read", "write", "edit", "view", "todo", "rem", "scheduler",
		"python", "web", "plugin", "sessions", "decide",
	})
	runDoing(t, tool)
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
	want := "bash,read,write,edit,view,rem,python,web"
	if got := allowArg(t, spawn.calls[0].Argv); got != want {
		t.Fatalf("the worker's allow = %q, want the doing set %q (todo, scheduler and plugin are the session's)", got, want)
	}
}

func TestADelegateSpawnKeepsOnlyWhatTheDoingSetShares(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	tool := doingTool(t, h, spawn, []string{"bash", "todo"})
	runDoing(t, tool)
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
	if got := allowArg(t, spawn.calls[0].Argv); got != "bash" {
		t.Fatalf("the worker's allow = %q, want bash alone (todo is the session's)", got)
	}
}

func TestADelegateSpawnThatKeepsNothingRunsAllowNone(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	for _, allow := range [][]string{nil, {"delegate"}, {"todo", "scheduler"}} {
		spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
		tool := doingTool(t, h, spawn, allow)
		runDoing(t, tool)
		if len(spawn.calls) != 1 {
			t.Fatalf("allow %v: spawn calls = %d, want 1", allow, len(spawn.calls))
		}
		if got := allowArg(t, spawn.calls[0].Argv); got != sched.NoToolsAllow {
			t.Fatalf("allow %v: the worker's allow = %q, want %q (a worker never holds more than its session)", allow, got, sched.NoToolsAllow)
		}
	}
}

func TestTheDelegateWordsNameTheDoingSet(t *testing.T) {
	words := delegate.New(delegate.Opts{}).Description()
	if !strings.Contains(words, "a worker holds only the doing set") ||
		!strings.Contains(words, "bash, read, write, edit, view, python, web, rem") {
		t.Fatalf("the delegate's words must name the doing set: %s", words)
	}
}
