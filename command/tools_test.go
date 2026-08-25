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
	"github.com/mrsirg97-rgb/rig/store/scope"
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
	db, _, _, err := store.Open(t.TempDir()+"/todo.sqlite", todostore.Statements(), todostore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

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

	if read != strings.TrimPrefix(created, "\u2192 queue replaced with 1 tasks\n") {
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

func TestSchedulerCommandNoFleetRefuses(t *testing.T) {
	file := "/home/op/.rig/workers.json"
	env := &command.Env{
		Workers: command.Workers{File: file},
		Tools:   map[string]core.Tool{},
	}
	out, err := runCmd(t, "scheduler", "list", env)
	if err == nil {
		t.Fatalf("no fleet: the command must refuse, got %q", out)
	}
	want := "scheduler: no workers configured (" + file + " names the model)"
	if err.Error() != want {
		t.Fatalf("the voice = %q, want %q (the command's, not the generic no-tool)", err.Error(), want)
	}
}

func TestSchedulerCommandMissingToolWithFleetKeepsTheGenericVoice(t *testing.T) {
	env := &command.Env{
		Workers: command.Workers{Model: "w", Slots: 1, File: "/x/workers.json", Configured: true},
		Tools:   map[string]core.Tool{},
	}
	_, err := runCmd(t, "scheduler", "list", env)
	if err == nil || !strings.Contains(err.Error(), "no scheduler tool (the root did not put it in Env.Tools)") {
		t.Fatalf("fleet configured but the tool absent: the generic voice must name the missing tool, got %v", err)
	}
}

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

	cur = after
	if _, err := runCmd(t, "todo", "read", env); err != nil {
		t.Fatal(err)
	}
	if saw.id != after.ID {
		t.Fatalf("post-swap, the fresh session must be threaded, got %q", saw.id)
	}
}

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

	updated, err := runCmd(t, "scheduler", "update j1 model brain prompt do the nightly thing", env)
	if err != nil || !strings.Contains(updated, "updated j1") {
		t.Fatalf("update must land in the store verbatim:\n%s\n%v", updated, err)
	}
	if _, err := runCmd(t, "scheduler", "update j1 cron 1 3 * * *", env); err != nil {
		t.Fatalf("cadence update: %v", err)
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
	_, err = runCmd(t, "scheduler", "update", env)
	if err == nil || !strings.Contains(err.Error(), "scheduler: update takes an id") {
		t.Fatalf("a bare update must refuse naming the shape, got %v", err)
	}
	_, err = runCmd(t, "scheduler", "update j1 bogus brain", env)
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("an unknown key must refuse, got %v", err)
	}
}

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

func schedStores(t *testing.T, home, cwd string) sched.DB {
	t.Helper()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	db, _, _, err := store.Open(home+"/global.sqlite", sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestTodoProjectCommand(t *testing.T) {
	db := openTodo(t)
	env := &command.Env{
		Session: func() *core.Session { return core.NewSession() },
		Tools:   map[string]core.Tool{"todo": todoapi.New(db)},
	}
	proj := t.TempDir()
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, todostore.Project{Key: scope.Key(proj), Label: scope.Label(proj)}, []todostore.CreateItem{{Text: "elsewhere"}}, "seed"); err != nil {
		t.Fatal(err)
	}
	rendered, err := runCmd(t, "todo", "project "+proj, env)
	if err != nil {
		t.Fatalf("todo project: %v", err)
	}
	if !strings.Contains(rendered, "elsewhere") {
		t.Fatalf("todo project must render that project's queue:\n%s", rendered)
	}

	unknown := t.TempDir()
	empty, err := runCmd(t, "todo", "project "+unknown, env)
	if err != nil {
		t.Fatalf("todo project unknown: %v", err)
	}
	if !strings.Contains(empty, "(no tasks in "+scope.Label(unknown)+"'s queue)") {
		t.Fatalf("an unknown path's empty queue must refuse naming its scope:\n%s", empty)
	}

	_, err = runCmd(t, "todo", "project", env)
	if err == nil || err.Error() != "todo: project takes a path (todo project <path>)" {
		t.Fatalf("a bare project must refuse naming the shape, got %v", err)
	}
}

func TestSchedulerUpdatePromptKeepsKeyWords(t *testing.T) {
	var got map[string]any
	capture := fakeExecFunc(func(ctx context.Context, args json.RawMessage) (string, error) {
		got = map[string]any{}
		if err := json.Unmarshal(args, &got); err != nil {
			return "", err
		}
		return "updated", nil
	})
	env := &command.Env{Session: func() *core.Session { return core.NewSession() }, Tools: map[string]core.Tool{"scheduler": capture}}
	if _, err := runCmd(t, "scheduler", "update j1 model brain prompt check the model config at 9", env); err != nil {
		t.Fatal(err)
	}
	if got["prompt"] != "check the model config at 9" || got["model"] != "brain" {
		t.Fatalf("prompt must take the rest of the line, key words included: %v", got)
	}
	if _, err := runCmd(t, "scheduler", "update j1 cron 1 3 * * * prompt x", env); err != nil {
		t.Fatal(err)
	}
	if got["cron"] != "1 3 * * *" || got["prompt"] != "x" {
		t.Fatalf("cron takes five fields, then prompt: %v", got)
	}
	if _, err := runCmd(t, "scheduler", "update j1 cron 1 3 *", env); err == nil || !strings.Contains(err.Error(), "five fields") {
		t.Fatalf("a short cron must refuse by name, got %v", err)
	}
}
