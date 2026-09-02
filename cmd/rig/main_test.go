package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/config"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/perm"
	"github.com/mrsirg97-rgb/rig/models"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state"
	"github.com/mrsirg97-rgb/rig/store/state/domain"
	"github.com/mrsirg97-rgb/rig/tool/bash"
	"github.com/mrsirg97-rgb/rig/tool/diff"
	"github.com/mrsirg97-rgb/rig/tool/file"
	"github.com/mrsirg97-rgb/rig/tool/fs"
	sessionstool "github.com/mrsirg97-rgb/rig/tool/sessions"
)

func TestVersionIsTheFreeze(t *testing.T) {

	if Version != "0.21.0" {
		t.Fatalf("Version = %q, want 0.21.0 (pre-1.0, feature-complete)", Version)
	}

	if !regexp.MustCompile(`^0\.\d+\.\d+$`).MatchString(Version) {
		t.Fatalf("Version %q must be 0.x.y until 1.0", Version)
	}
}

type nullFrontend struct{}

type fakeTodo struct{}

func (fakeTodo) Name() string { return "todo" }
func (fakeTodo) Description() string {
	return "fake todo surface"
}
func (fakeTodo) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakeTodo) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "", nil
}

type fakeRem struct{}

func (fakeRem) Name() string { return "rem" }
func (fakeRem) Description() string {
	return "fake rem surface"
}
func (fakeRem) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakeRem) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "", nil
}

type fakeSched struct{}

func (fakeSched) Name() string { return "scheduler" }
func (fakeSched) Description() string {
	return "fake scheduler surface"
}
func (fakeSched) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakeSched) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "", nil
}

type fakeDelegate struct{}

func (fakeDelegate) Name() string { return "delegate" }
func (fakeDelegate) Description() string {
	return "fake delegate surface"
}
func (fakeDelegate) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakeDelegate) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "", nil
}

type fakePython struct{}

func (fakePython) Name() string { return "python" }
func (fakePython) Description() string {
	return "fake python kernel surface"
}
func (fakePython) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakePython) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "", nil
}

type fakeWebSearch struct{}

func (fakeWebSearch) Name() string { return "web_search" }
func (fakeWebSearch) Description() string {
	return "fake web_search surface"
}
func (fakeWebSearch) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakeWebSearch) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "", nil
}

type fakeWebFetch struct{}

func (fakeWebFetch) Name() string { return "web_fetch" }
func (fakeWebFetch) Description() string {
	return "fake web_fetch surface"
}
func (fakeWebFetch) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakeWebFetch) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "", nil
}

func (nullFrontend) Input(ctx context.Context) (string, error) { return "", io.EOF }
func (nullFrontend) Notify(ev core.Event)                      {}

type oneLineFrontend struct{ line string }

func (f *oneLineFrontend) Input(ctx context.Context) (string, error) {
	if f.line == "" {
		return "", io.EOF
	}
	l := f.line
	f.line = ""
	return l, nil
}
func (*oneLineFrontend) Notify(ev core.Event) {}

func testTools() map[string]core.Tool {
	return map[string]core.Tool{
		"bash": bash.New(), "read": file.Read(), "write": file.Write(), "edit": file.Edit(),
		"ls": fs.LS(), "find": fs.Find(), "grep": fs.Grep(),
		"todo": fakeTodo{}, "rem": fakeRem{}, "scheduler": fakeSched{}, "delegate": fakeDelegate{}, "python": fakePython{},
		"web_search": fakeWebSearch{}, "web_fetch": fakeWebFetch{},

		"diff": diff.New(store.DB{}), "sessions": sessionstool.New("", ""),

		"plugins": fakePlugins{},
	}
}

type fakePlugins struct{}

func (fakePlugins) Name() string            { return "plugins" }
func (fakePlugins) Description() string     { return "the fixture plugins surface" }
func (fakePlugins) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakePlugins) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "plugins: reload: 0 loaded, 0 skipped", nil
}

func testRoot(fe core.Frontend) *root {
	sess := core.NewSession()
	r := &root{
		baseURL:  "http://127.0.0.1:8080/v1",
		system:   "be terse",
		allow:    []string{"bash", "read", "write", "edit"},
		retries:  3,
		fe:       fe,
		sdb:      store.DB{},
		remDB:    store.DB{},
		cwd:      "",
		activeID: "local",
		row:      defaultRow(),
		runtime:  defaultsTableValue(),
		session:  sess,
		tools:    testTools(),
	}
	r.rec = state.NewRecorder(fe, store.DB{}, "", "local", Version, sess.ID, sess)
	return r
}

func TestWireRegistersEverySeam(t *testing.T) {
	k := wire(testRoot(nullFrontend{}))
	if k == nil {
		t.Fatal("wire returned nil")
	}
	if k.Provider == nil || k.Frontend == nil || k.Policy == nil {
		t.Fatal("every required seam must be registered")
	}
	want := []string{"bash", "diff", "edit", "find", "grep", "ls", "plugin", "plugins", "python", "read", "rem", "sessions", "todo", "web_fetch", "web_search", "write"}
	if got := k.SortedToolNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("no fleet: the worker tools must stay unregistered: %v, want %v", got, want)
	}
	if len(k.Middleware) != 7 {
		t.Fatalf("middleware = %d links, want the path boundary, the router, the provenance rule, the allow-list, the bound, the round cap, and the result bound (SPEC_PLUGINS 8's seam; SPEC_SANDBOX 2; SPEC_HARDENING decision 9; the observation tap is retired: the loop's events are the source)", len(k.Middleware))
	}
}

func TestWireWithAConfiguredFleetRegistersTheWorkerTools(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.workers = &config.Workers{Model: "local", Slots: 1}
	k := wire(r)
	want := []string{"bash", "delegate", "diff", "edit", "find", "grep", "ls", "plugin", "plugins", "python", "read", "rem", "scheduler", "sessions", "todo", "web_fetch", "web_search", "write"}
	if got := k.SortedToolNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("a configured fleet must register the worker tools: %v, want %v", got, want)
	}
}

func TestSessionsIsANonMutatingNative(t *testing.T) {
	inNatives := false
	for _, n := range nativeToolNames {
		if n == "sessions" {
			inNatives = true
		}
	}
	if !inNatives {
		t.Fatal("sessions must be a native (the eighteenth)")
	}
	if mutatingNatives["sessions"] {
		t.Fatal("sessions must be absent from mutatingNatives (read-only: it never pauses)")
	}
	r := &root{}
	r.natives = make(map[string]bool, len(nativeToolNames))
	for _, n := range nativeToolNames {
		r.natives[n] = true
	}
	if r.isMutating("sessions") {
		t.Fatal("sessions must pass the approval gate silently (isMutating false)")
	}
	if !r.isMutating("bash") {
		t.Fatal("the control: bash must stay mutating")
	}
}

type guidelineMW struct {
	core.ToolMiddlewareFunc
	text string
}

func (g guidelineMW) Guidelines() string { return g.text }

func TestGuidelinesAreCollectedIntoTheSystemPrompt(t *testing.T) {
	gw := guidelineMW{ToolMiddlewareFunc: func(next core.ToolExec) core.ToolExec { return next }, text: "a tool that keeps failing in a turn is refused at the bound; read its error."}
	plain := core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec { return next })

	got := guidelinesOf([]core.ToolMiddleware{perm.Allowlist("bash"), gw, plain})
	if got != gw.text {
		t.Fatalf("the contributor's prose must land verbatim: %q", got)
	}
	if got := guidelinesOf([]core.ToolMiddleware{perm.Allowlist("bash"), plain}); got != "" {
		t.Fatalf("no contributors: the collection must be empty, got %q", got)
	}
	a := guidelineMW{ToolMiddlewareFunc: func(next core.ToolExec) core.ToolExec { return next }, text: "one"}
	b := guidelineMW{ToolMiddlewareFunc: func(next core.ToolExec) core.ToolExec { return next }, text: "two"}
	if got := guidelinesOf([]core.ToolMiddleware{a, b}); got != "one\n\ntwo" {
		t.Fatalf("multiple contributors must join in listed order: %q", got)
	}
}

func TestWireSystemPromptCarriesTheBase(t *testing.T) {
	k := wire(testRoot(nullFrontend{}))
	msgs, err := k.Policy.Assemble(context.Background(), core.NewSession())
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 || msgs[0].Role != core.RoleSystem || msgs[0].Content != "be terse" {
		t.Fatalf("the base system prompt must ride the policy verbatim: %+v", msgs)
	}
}

func TestOneShotAndResumeRefuseAtConstruction(t *testing.T) {
	if err := checkOneShot("", ""); err != nil {
		t.Fatalf("the bare REPL must pass: %v", err)
	}
	if err := checkOneShot("prompt", ""); err != nil {
		t.Fatalf("one-shot alone must pass: %v", err)
	}
	if err := checkOneShot("", "res-1"); err != nil {
		t.Fatalf("resume alone must pass: %v", err)
	}
	err := checkOneShot("prompt", "res-1")
	if !errors.Is(err, ErrResumeWithPrompt) {
		t.Fatalf("-p and -resume must refuse at construction, got %v", err)
	}
}

func TestSessionIDAndResumeRefuseAtConstruction(t *testing.T) {
	if err := checkSessionID("worker", ""); err != nil {
		t.Fatalf("a worker identity alone must pass: %v", err)
	}
	if err := checkSessionID("", "res-1"); err != nil {
		t.Fatalf("resume alone must pass: %v", err)
	}
	if !errors.Is(checkSessionID("worker", "res-1"), ErrSessionIDWithResume) {
		t.Fatal("a worker identity and resume must refuse at construction")
	}
}

func TestSessionForResumesOrStartsFresh(t *testing.T) {
	fresh, err := sessionFor("", nil)
	if err != nil || fresh.ID == "" {
		t.Fatalf("no -resume: a fresh session with a minted id, got %v %v", fresh, err)
	}
	resume := func(id string) (*core.Session, error) {
		if id != "res-1" {
			return nil, errors.New("the wrong id was requested")
		}
		return &core.Session{ID: id, Files: map[string]core.FileState{}}, nil
	}
	resumed, err := sessionFor("res-1", resume)
	if err != nil || resumed.ID != "res-1" {
		t.Fatalf("the resumed session must keep its identity, got %v %v", resumed, err)
	}
	_, err = sessionFor("nope", func(id string) (*core.Session, error) {
		return nil, fmt.Errorf("resume: no such session: %s", id)
	})
	if err == nil || !strings.Contains(err.Error(), "no such session") {
		t.Fatalf("an unknown id must fail loud naming the gap, got %v", err)
	}
}

func TestResumePathAdoptsTheSessionIdentity(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	sid := "adopt-me"
	if e := state.RecordSession(ctx, db, sid, "/tmp/wt", "model-x", "0.1.0"); e != nil {
		t.Fatal(e)
	}
	if _, e := state.RecordMessage(ctx, db, sid, "user", "before the kill", nil, nil); e != nil {
		t.Fatal(e)
	}
	sess, err := state.Resume(ctx, db, sid)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}

	rec := state.NewRecorder(&oneLineFrontend{line: "resumed"}, db, "/tmp/wt", "model-x", Version, sess.ID, sess)
	if text, err := rec.Input(ctx); err != nil || text != "resumed" {
		t.Fatalf("input on the resumed session: %q %v", text, err)
	}
	if e := rec.Close("ok"); e != nil {
		t.Fatal(e)
	}

	first := mustReadMsg(t, db, 1)
	if first.Role != "user" || first.Content != "before the kill" {
		t.Fatalf("the seeded transcript must survive the adoption: %+v", first)
	}
	second := mustReadMsg(t, db, 2)
	if second.Role != "user" {
		t.Fatalf("the resumed turn must append after the seeded rows: %+v", second)
	}
	s := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewSessionDomain().GetSession(c, sid).Row()
	}).(*domain.Session)
	if s == nil || s.Id != sid || s.Exit != "ok" {
		t.Fatalf("one identity, closed: %+v", s)
	}
}

func defaultRow() models.Model {
	return models.Model{Role: models.RoleInteractive, ID: "local", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384}
}

func defaultsTable(t *testing.T) models.Table {
	t.Helper()
	cfg, err := config.Load(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg.Models
}

func defaultsTableValue() models.Table {
	dir := filepath.Join(os.TempDir(), "rig-test-nodir")
	cfg, err := config.Load(dir, dir)
	if err != nil {
		panic("rig: defaultsTableValue: " + err.Error())
	}
	return cfg.Models
}

func mustRead(t *testing.T, db store.DB, fn func(context.Context) (any, error)) any {
	t.Helper()
	c, tx, err := db.Tx(context.Background())
	if err != nil {
		t.Fatalf("read tx: %v", err)
	}
	defer tx.Rollback()
	out, err := fn(c)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustReadMsg(t *testing.T, db store.DB, seq int64) domain.Message {
	t.Helper()
	c, tx, err := db.Tx(context.Background())
	if err != nil {
		t.Fatalf("read tx: %v", err)
	}
	defer tx.Rollback()
	m, err := domain.NewMessageDomain().GetMessage(c, seq).Row()
	if err != nil {
		t.Fatal(err)
	}
	return *m
}

func TestDefaultRoleIsByteIdentical(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.agents = "G\n\nP"
	got := r.buildSystem()
	want := "be terse" + "\n\n" + "G\n\nP"
	if got != want {
		t.Fatalf("the default assembly = %q, want today's bytes %q (the role sits between system and AGENTS.md only when set)", got, want)
	}
}

func TestRoleAssemblySitsBetweenSystemAndAgents(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.agents = "G\n\nP"
	r.role = "architect"
	got := r.buildSystem()
	want := "be terse" + "\n\n" + command.RoleProse("architect") + "\n\n" + "G\n\nP"
	if got != want {
		t.Fatalf("the architect assembly = %q, want %q (system, the stance, then AGENTS.md)", got, want)
	}
}

func TestRoleSwitchRebuildsThePair(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.agents = "G\n\nP"
	wire(r)
	before := r.fullSystem
	if err := r.switchRole(context.Background(), "architect"); err != nil {
		t.Fatalf("switchRole: %v", err)
	}
	if r.fullSystem == before || !strings.Contains(r.fullSystem, "architect") {
		t.Fatalf("the switch must recompute the assembly with the stance: %q", r.fullSystem)
	}
	msgs, err := r.k.Policy.Assemble(context.Background(), core.NewSession())
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 || msgs[0].Role != core.RoleSystem || !strings.Contains(msgs[0].Content, "architect") {
		t.Fatalf("the rebuilt pair's request must carry the stance: %+v", msgs)
	}
}

func TestRoleSwitchDefaultInjectsNothing(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.agents = "G\n\nP"
	wire(r)
	if err := r.switchRole(context.Background(), "architect"); err != nil {
		t.Fatal(err)
	}
	if err := r.switchRole(context.Background(), "default"); err != nil {
		t.Fatal(err)
	}
	got := r.fullSystem
	want := "be terse" + "\n\n" + "G\n\nP"
	if got != want {
		t.Fatalf("back to default = %q, want today's bytes %q", got, want)
	}
}

func TestModelSwitchResetsAForeignEffort(t *testing.T) {
	r := testRoot(nullFrontend{})
	speaks, err := models.New(
		models.Model{Role: models.RoleInteractive, ID: "local", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Efforts: []string{"low", "medium", "xhigh"}},
		models.Model{Role: models.RoleInteractive, ID: "onlymax", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Efforts: []string{"max"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	r.runtime = speaks
	wire(r)

	r.effort = "xhigh"
	note, err := r.switchModel(context.Background(), "onlymax")
	if err != nil {
		t.Fatalf("switchModel: %v", err)
	}
	if r.effort != "" {
		t.Fatalf("a level the new row does not name must reset, kept %q", r.effort)
	}
	want := `effort: "xhigh" is not a level for onlymax — reset to server default`
	if note != want {
		t.Fatalf("the reset must be the switch's note:\ngot  %q\nwant %q", note, want)
	}

	r.effort = "max"
	note, err = r.switchModel(context.Background(), "onlymax")
	if err != nil {
		t.Fatalf("switchModel same: %v", err)
	}
	if r.effort != "max" || note != "" {
		t.Fatalf("a level the row names must survive the switch silently, got (%q, %q)", r.effort, note)
	}
}

func TestApproveDialDoorRule(t *testing.T) {
	r := testRoot(nullFrontend{})
	if err := r.switchApprove(context.Background(), "manual"); err == nil ||
		!strings.Contains(err.Error(), "ask door") {
		t.Fatalf("manual without a door must refuse: %v", err)
	}
	r.askDoor = func(ctx context.Context, prompt string) bool { return true }
	if err := r.switchApprove(context.Background(), "manual"); err != nil || r.approve != "manual" {
		t.Fatalf("manual with a door must set: (%v, %q)", err, r.approve)
	}
	if err := r.switchApprove(context.Background(), "auto"); err != nil || r.approve != "auto" {
		t.Fatalf("auto must set: (%v, %q)", err, r.approve)
	}
	if err := r.switchApprove(context.Background(), "yolo"); err == nil {
		t.Fatal("an unknown mode must refuse")
	}
}

func TestNewResetsApproveToTheSettingsDefault(t *testing.T) {
	h := newHarness(t, defaultRow(), "local", defaultsTable(t))
	h.r.approveDefault = "manual"
	h.r.askDoor = func(ctx context.Context, prompt string) bool { return true }
	h.r.approve = "auto"
	if _, err := h.r.newSession(context.Background()); err != nil {
		t.Fatalf("newSession: %v", err)
	}
	if h.r.approve != "manual" {
		t.Fatalf("/new must reset to the settings default: %q", h.r.approve)
	}
}

func TestIsMutatingPredicate(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.natives = map[string]bool{}
	for _, n := range nativeToolNames {
		r.natives[n] = true
	}
	for _, n := range []string{"bash", "write", "edit", "python", "scheduler", "plugins", "delegate", "gpu_stats"} {
		if !r.isMutating(n) {
			t.Errorf("%s must pause (a mutating native, or a plugin)", n)
		}
	}
	for _, n := range []string{"read", "ls", "find", "grep", "web_search", "web_fetch", "todo", "rem", "diff"} {
		if r.isMutating(n) {
			t.Errorf("%s must pass silently", n)
		}
	}
}

func TestSwapInKeepsDialsAndRebuilds(t *testing.T) {
	r := testRoot(nullFrontend{})
	wire(r)
	r.effort = "xhigh"
	r.role = "architect"
	s2 := core.NewSession()
	rec2 := state.NewRecorder(r.fe, r.sdb, r.cwd, r.activeID, Version, s2.ID, s2)
	r.swapIn(s2, rec2)
	if r.effort != "xhigh" || r.role != "architect" {
		t.Fatalf("a resume must keep the dials, got effort=%q role=%q", r.effort, r.role)
	}
	if !strings.Contains(r.fullSystem, "architect") {
		t.Fatalf("the handoff must recompute the assembly with the stance: %q", r.fullSystem)
	}
}

func TestNewResetsDials(t *testing.T) {
	h := newHarness(t, defaultRow(), "local", defaultsTable(t))
	h.r.effort = "xhigh"
	h.r.role = "architect"
	if _, err := h.r.newSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.r.effort != "" || h.r.role != "" {
		t.Fatalf("/new must reset the dials, got effort=%q role=%q", h.r.effort, h.r.role)
	}
	if strings.Contains(h.r.fullSystem, "architect") {
		t.Fatalf("the fresh assembly must drop the stance: %q", h.r.fullSystem)
	}
}

func TestEffortForWireFallsBackToTheRow(t *testing.T) {
	r := testRoot(nullFrontend{})
	if got := r.effortForWire(); got != "" {
		t.Fatalf("no dial, no row default: today's bytes, got %q", got)
	}
	r.row.Effort = "xhigh"
	if got := r.effortForWire(); got != "xhigh" {
		t.Fatalf("the row's default must ride the wire: %q", got)
	}
	r.effort = "low"
	if got := r.effortForWire(); got != "low" {
		t.Fatalf("the dial must override the row: %q", got)
	}
}
