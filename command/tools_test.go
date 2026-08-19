package command_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
	schedapi "github.com/mrsirg97-rgb/rig/tool/scheduler"
	todoapi "github.com/mrsirg97-rgb/rig/tool/todo"
)

func runCmd(t *testing.T, name, args string, env *command.Env) (string, error) {
	t.Helper()
	byName := allByName(t)
	cmd, ok := byName[name]
	if !ok {
		t.Fatalf("no such command: %s", name)
	}
	return cmd.Run(context.Background(), args, env)
}

func openTodo(t *testing.T) store.DB {
	t.Helper()
	db, _, err := store.Open(t.TempDir()+"/todo.sqlite", todostore.Statements(), todostore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// TestTodoCommandRoundTrip (SPEC_COMMANDS, named): the tool's own voice,
// verbatim — create, read, start, the no-task refusal, the bare-todo
// refusal, and the adapter's shape refusal.
func TestTodoCommandRoundTrip(t *testing.T) {
	s := core.NewSession()
	env := &command.Env{
		Session: func() *core.Session { return s },
		Tools:   map[string]core.Tool{"todo": todoapi.New(openTodo(t))},
	}

	created, err := runCmd(t, "todo", "create write the spec", env)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(created, "write the spec") || !strings.Contains(created, "t1") {
		t.Fatalf("the create reply must carry the task verbatim:\n%s", created)
	}

	read, err := runCmd(t, "todo", "read", env)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// the create reply carries its note above the queue; the read is the
	// queue itself — the same queue, the model's and the user's.
	if read != strings.TrimPrefix(created, "\u2192 queue replaced: 1 tasks\n") {
		t.Fatalf("read must be the same queue the create reported:\ncreate:\n%s\nread:\n%s", created, read)
	}

	started, err := runCmd(t, "todo", "start t1", env)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(started, "'t1' started") || !strings.Contains(started, "t1 [~]") {
		t.Fatalf("the start reply must show the state change verbatim:\n%s", started)
	}

	_, err = runCmd(t, "todo", "start t9", env)
	if err == nil || !strings.Contains(err.Error(), "no task 't9'") {
		t.Fatalf("the tool's no-task refusal must pass through verbatim, got %v", err)
	}

	_, err = runCmd(t, "todo", "", env)
	if err == nil || !strings.Contains(err.Error(), "action required") {
		t.Fatalf("a bare todo must be the tool's own 'action required', got %v", err)
	}

	_, err = runCmd(t, "todo", "start t1 extra", env)
	if err == nil || err.Error() != "todo: start takes an id (todo start <id>)" {
		t.Fatalf("extras must be the adapter's shape refusal naming the shape, got %v", err)
	}
}

// TestToolCommandThreadsTheLiveSession (SPEC_COMMANDS, named): the Exec
// ctx carries the live session (post-`/new`) — the same attribution the
// model's calls get.
func TestToolCommandThreadsTheLiveSession(t *testing.T) {
	type seen struct {
		mu   sync.Mutex
		id   string
		have bool
	}
	saw := &seen{}
	fake := fakeExecFunc(func(ctx context.Context, args json.RawMessage) (string, error) {
		s, ok := core.SessionFrom(ctx)
		saw.mu.Lock()
		defer saw.mu.Unlock()
		saw.have = ok
		if ok {
			saw.id = s.ID
		}
		return "ok", nil
	})

	old := core.NewSession()
	after := core.NewSession()
	var cur *core.Session = old
	env := &command.Env{
		Session: func() *core.Session { return cur },
		Tools:   map[string]core.Tool{"todo": fake},
	}
	if _, err := runCmd(t, "todo", "read", env); err != nil {
		t.Fatal(err)
	}
	if !saw.have || saw.id != old.ID {
		t.Fatalf("the live session must be threaded, got %q (have=%v)", saw.id, saw.have)
	}

	cur = after // the /new swap
	if _, err := runCmd(t, "todo", "read", env); err != nil {
		t.Fatal(err)
	}
	if saw.id != after.ID {
		t.Fatalf("post-swap, the fresh session must be threaded, got %q", saw.id)
	}
}

// fakeTool is a core.Tool that delegates Exec to a function, for the
// threading test.
type fakeTool struct {
	exec func(ctx context.Context, args json.RawMessage) (string, error)
}

func (f fakeTool) Name() string            { return "todo" }
func (f fakeTool) Description() string     { return "fake" }
func (f fakeTool) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (f fakeTool) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return f.exec(ctx, args)
}

func fakeExecFunc(f func(ctx context.Context, args json.RawMessage) (string, error)) fakeTool {
	return fakeTool{exec: f}
}

// TestSchedulerCommandRoundTrip (SPEC_COMMANDS, named): create (vixie
// and once), list, pause/resume/remove, runs — the tool's voice,
// verbatim, over the real stores.
func TestSchedulerCommandRoundTrip(t *testing.T) {
	home := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ct := newFakeCron()
	st := schedStores(t, home, cwd)
	tool := schedapi.New(st, ct, "/bin/true run-job", "qwen3.8-workers")
	env := &command.Env{
		Session: func() *core.Session { return core.NewSession() },
		Tools:   map[string]core.Tool{"scheduler": tool},
	}

	created, err := runCmd(t, "scheduler", "create nightly report 0 3 * * *", env)
	if err != nil {
		t.Fatalf("create: %v (%s)", err, created)
	}
	if !strings.Contains(created, "j1") {
		t.Fatalf("the create reply must name the job id:\n%s", created)
	}
	list, err := runCmd(t, "scheduler", "list", env)
	if err != nil || !strings.Contains(list, "nightly") {
		t.Fatalf("list must show the job verbatim:\n%s\n%v", list, err)
	}

	once, err := runCmd(t, "scheduler", "create once-job do it once 2030-01-01T00:00:00Z", env)
	if err != nil {
		t.Fatalf("once create: %v (%s)", err, once)
	}
	if !strings.Contains(once, "j2") {
		t.Fatalf("the once create must name the job id:\n%s", once)
	}

	if _, err := runCmd(t, "scheduler", "pause j1", env); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := runCmd(t, "scheduler", "resume j1", env); err != nil {
		t.Fatalf("resume: %v", err)
	}
	runs, err := runCmd(t, "scheduler", "runs j1 2", env)
	if err != nil || !strings.Contains(runs, "0 runs") {
		t.Fatalf("runs with no runs must carry the no-runs voice:\n%s\n%v", runs, err)
	}
	if _, err := runCmd(t, "scheduler", "remove j1", env); err != nil {
		t.Fatalf("remove: %v", err)
	}

	_, err = runCmd(t, "scheduler", "list extra", env)
	if err == nil || err.Error() != "scheduler: list takes no args (scheduler list)" {
		t.Fatalf("list extras must be the shape refusal, got %v", err)
	}
	_, err = runCmd(t, "scheduler", "create short", env)
	if err == nil || err.Error() != "scheduler: create needs name, prompt, and a cron (5-field, or 'once' <ISO>)" {
		t.Fatalf("a short create must refuse naming the shape, got %v", err)
	}
	_, err = runCmd(t, "scheduler", "runs j2 x", env)
	if err == nil || err.Error() != `scheduler: "x": not an integer (scheduler runs <id> [n])` {
		t.Fatalf("a non-integer n must refuse, got %v", err)
	}
}

// fakeCron is the scheduler's crontab seam at the test bench.
type fakeCron struct {
	mu   sync.Mutex
	text string
}

func newFakeCron() *fakeCron { return &fakeCron{text: "SHELL=/bin/bash\n"} }

func (f *fakeCron) List() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text, nil
}

func (f *fakeCron) Install(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.text = text
	return nil
}

func (f *fakeCron) text_() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text
}

func schedStores(t *testing.T, home, cwd string) sched.Stores {
	t.Helper()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	global, _, err := store.Open(home+"/global.sqlite", sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	cw, _, err := store.Open(home+"/cwd.sqlite", sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	return sched.Stores{Global: global, Cwd: cw}
}

// TestTodoAddDoorsByteEqual (SPEC_UX 1, named): the add line parses to
// the tool's add action, and the two doors — the command's line and the
// model's call — carry the same reply, byte-equal (decision 6's rule:
// the shared renderer's input is the tool's own voice).
func TestTodoAddDoorsByteEqual(t *testing.T) {
	s := core.NewSession()
	toolDB := openTodo(t)
	cmdDB := openTodo(t)
	env := &command.Env{
		Session: func() *core.Session { return s },
		Tools:   map[string]core.Tool{"todo": todoapi.New(cmdDB)},
	}
	out, err := runCmd(t, "todo", "add wire the guard", env)
	if err != nil {
		t.Fatalf("add line: %v", err)
	}
	payload, err := json.Marshal(map[string]any{"action": "add", "text": "wire the guard"})
	if err != nil {
		t.Fatal(err)
	}
	toolReply, err := todoapi.New(toolDB).Exec(context.Background(), payload)
	if err != nil {
		t.Fatalf("add action: %v", err)
	}
	if out != toolReply {
		t.Fatalf("the two doors must be byte-equal below the opening line:\n[cmd]\n%s\n[tool]\n%s", out, toolReply)
	}
	if !strings.Contains(out, "t1 added: wire the guard") {
		t.Fatalf("the add voice must carry the minted id and the text:\n%s", out)
	}
}

// TestTodoAddLineShape is the adapter's shape refusals for the add line.
func TestTodoAddLineShape(t *testing.T) {
	s := core.NewSession()
	env := &command.Env{
		Session: func() *core.Session { return s },
		Tools:   map[string]core.Tool{"todo": todoapi.New(openTodo(t))},
	}
	_, err := runCmd(t, "todo", "add", env)
	if err == nil || err.Error() != "todo: add needs text (todo add <text…>)" {
		t.Fatalf("a bare add must name the shape, got: %v", err)
	}
}
