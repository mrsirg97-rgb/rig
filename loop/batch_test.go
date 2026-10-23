package loop_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/loop"
	"github.com/mrsirg97-rgb/rig/v3/tool/file"
)

type timedTool struct {
	name   string
	cur    atomic.Int32
	peak   atomic.Int32
	mu     sync.Mutex
	starts map[string]time.Time
	ends   map[string]time.Time
	cancel context.CancelFunc
	fails  map[string]bool
}

func newTimed(name string) *timedTool {
	return &timedTool{name: name, starts: map[string]time.Time{}, ends: map[string]time.Time{}}
}

func (t *timedTool) Name() string            { return t.name }
func (t *timedTool) Description() string     { return "timed test tool" }
func (t *timedTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (t *timedTool) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID string `json:"id"`
		Ms int    `json:"ms"`
	}
	_ = json.Unmarshal(args, &a)
	if t.fails[a.ID] {
		return "", fmt.Errorf("%s: %s failed", t.name, a.ID)
	}
	t.mu.Lock()
	t.starts[a.ID] = time.Now()
	t.mu.Unlock()
	n := t.cur.Add(1)
	for {
		p := t.peak.Load()
		if n <= p || t.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if t.cancel != nil {
		t.cancel()
	}
	select {
	case <-time.After(time.Duration(a.Ms) * time.Millisecond):
	case <-ctx.Done():
	}
	t.cur.Add(-1)
	t.mu.Lock()
	t.ends[a.ID] = time.Now()
	t.mu.Unlock()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return t.name + ":" + a.ID, nil
}

func timedCall(id, name string, ms int) core.ToolCall {
	return core.ToolCall{ID: id, Name: name, Args: json.RawMessage(`{"id":"` + id + `","ms":` + itoa(ms) + `}`)}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func batchKernel(t *testing.T, tools []core.Tool, calls []core.ToolCall, opts ...rig.Option) (rig.Kernel, *recorderFrontend, *core.Session) {
	t.Helper()
	evs := make([]evt.Signal, 0, len(calls)+1)
	for _, c := range calls {
		evs = append(evs, callEv(c))
	}
	evs = append(evs, doneEv())
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: evs},
		{events: []evt.Signal{textEv("done"), doneEv()}},
	}}
	f := &recorderFrontend{inputs: make(chan string, 1)}
	s := core.NewSession()
	base := []rig.Option{rig.WithProvider(p), rig.WithFrontend(f), rig.WithPolicy(&transcriptPolicy{}), rig.WithTools(tools...)}
	k := rig.New(append(append(base, rig.WithSession(s)), opts...)...)
	f.inputs <- "go"
	close(f.inputs)
	return k, f, s
}

func resultOrder(f *recorderFrontend) []string {
	var ids []string
	for _, ev := range f.events {
		if r, ok := ev.(core.ToolResult); ok {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

func toolOrder(s *core.Session) []string {
	var ids []string
	for _, m := range s.Messages {
		if m.Role == core.RoleTool {
			ids = append(ids, m.ToolID)
		}
	}
	return ids
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBatchRunsReadsConcurrentlyAndEmitsInCallOrder(t *testing.T) {
	read := newTimed("read")
	calls := []core.ToolCall{timedCall("c1", "read", 90), timedCall("c2", "read", 40), timedCall("c3", "read", 10)}
	k, f, s := batchKernel(t, []core.Tool{read}, calls, rig.WithPriority(writesFirst))
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{"c1", "c2", "c3"}
	if got := resultOrder(f); !same(got, want) {
		t.Fatalf("ToolResult order %v, want call order %v", got, want)
	}
	if got := toolOrder(s); !same(got, want) {
		t.Fatalf("transcript order %v, want call order %v", got, want)
	}
	if read.peak.Load() < 2 {
		t.Fatalf("peak in flight %d: the reads did not overlap", read.peak.Load())
	}
	var d1, d3 time.Duration
	for _, ev := range f.events {
		if r, ok := ev.(core.ToolResult); ok {
			if r.ID == "c1" {
				d1 = r.Duration
			}
			if r.ID == "c3" {
				d3 = r.Duration
			}
		}
	}
	if d3 >= d1 {
		t.Fatalf("Duration is each call's own: c3 %v should be under c1 %v", d3, d1)
	}
}

func TestBatchMutatingCallIsABarrier(t *testing.T) {
	read, write := newTimed("read"), newTimed("write")
	calls := []core.ToolCall{timedCall("c1", "read", 60), timedCall("c2", "read", 20), timedCall("c3", "write", 10), timedCall("c4", "read", 10)}
	k, f, s := batchKernel(t, []core.Tool{read, write}, calls, rig.WithPriority(writesFirst))
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{"c1", "c2", "c3", "c4"}
	if got := resultOrder(f); !same(got, want) {
		t.Fatalf("ToolResult order %v, want %v", got, want)
	}
	if got := toolOrder(s); !same(got, want) {
		t.Fatalf("transcript order %v, want %v", got, want)
	}
	if read.peak.Load() < 2 {
		t.Fatalf("c1 and c2 must overlap, peak %d", read.peak.Load())
	}
	if read.starts["c1"].Before(write.starts["c3"]) {
		t.Fatal("the write must outrank the reads: it must start first")
	}
	if read.starts["c1"].Before(write.ends["c3"]) || read.starts["c2"].Before(write.ends["c3"]) || read.starts["c4"].Before(write.ends["c3"]) {
		t.Fatal("the reads must start after the write finished")
	}
}
func TestBatchWithoutAPredicateIsSequential(t *testing.T) {
	read := newTimed("read")
	calls := []core.ToolCall{timedCall("c1", "read", 30), timedCall("c2", "read", 30), timedCall("c3", "read", 30)}
	k, f, _ := batchKernel(t, []core.Tool{read}, calls)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if read.peak.Load() != 1 {
		t.Fatalf("peak %d, want 1 (sequential)", read.peak.Load())
	}
	if got := resultOrder(f); !same(got, []string{"c1", "c2", "c3"}) {
		t.Fatalf("order %v", got)
	}
}

func TestBatchParallelBoundsTheRun(t *testing.T) {
	read := newTimed("read")
	calls := []core.ToolCall{timedCall("c1", "read", 40), timedCall("c2", "read", 40), timedCall("c3", "read", 40), timedCall("c4", "read", 40)}
	k, f, _ := batchKernel(t, []core.Tool{read}, calls, rig.WithPriority(writesFirst), rig.WithParallel(2))
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if read.peak.Load() != 2 {
		t.Fatalf("peak %d, want exactly 2", read.peak.Load())
	}
	if got := resultOrder(f); !same(got, []string{"c1", "c2", "c3", "c4"}) {
		t.Fatalf("order %v", got)
	}
}

func TestBatchCancellationMidRunDrainsTheBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	read := newTimed("read")
	read.cancel = cancel
	calls := []core.ToolCall{timedCall("c1", "read", 30), timedCall("c2", "read", 30)}
	evs := []evt.Signal{callEv(calls[0]), callEv(calls[1]), doneEv()}
	p := &scriptedProvider{turns: []scriptedTurn{{events: evs}, {holdCtx: true}}}
	f := &recorderFrontend{inputs: make(chan string, 1)}
	s := core.NewSession()
	k := rig.New(rig.WithProvider(p), rig.WithFrontend(f), rig.WithPolicy(&transcriptPolicy{}), rig.WithTools(read), rig.WithPriority(writesFirst), rig.WithSession(s))
	f.inputs <- "go"
	close(f.inputs)
	if err := loop.Run(ctx, k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := toolOrder(s); !same(got, []string{"c1", "c2"}) {
		t.Fatalf("the batch must drain into the transcript in order, got %v", got)
	}
	if got := resultOrder(f); !same(got, []string{"c1", "c2"}) {
		t.Fatalf("both results must be emitted, got %v", got)
	}
}

func TestBatchGuardCountsConcurrentFailures(t *testing.T) {
	calls := make([]core.ToolCall, 0, 6)
	for i := 0; i < 6; i++ {
		calls = append(calls, core.ToolCall{ID: "c" + itoa(i+1), Name: "read", Args: json.RawMessage(`{"same":true}`)})
	}
	failing := &failTool{name: "read"}
	counted := &countingGuard{}
	k, f, _ := batchKernel(t, []core.Tool{failing}, calls, rig.WithPriority(writesFirst), rig.WithMiddleware(counted))
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if counted.n.Load() != 6 {
		t.Fatalf("the chain must see every call of the run: %d", counted.n.Load())
	}
	if got := resultOrder(f); len(got) != 6 {
		t.Fatalf("six results, got %d", len(got))
	}
}

type countingGuard struct{ n atomic.Int32 }

func (g *countingGuard) Wrap(next core.ToolExec) core.ToolExec {
	return func(ctx context.Context, call core.ToolCall) (string, error) {
		g.n.Add(1)
		return next(ctx, call)
	}
}

type failTool struct {
	name string
	n    atomic.Int32
}

func (t *failTool) Name() string            { return t.name }
func (t *failTool) Description() string     { return "always fails" }
func (t *failTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *failTool) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	t.n.Add(1)
	return "synthetic failure", errors.New("synthetic failure")
}

func TestBatchSurfacesAPanickingToolAsAToolError(t *testing.T) {
	k, f, _ := batchKernel(t, []core.Tool{panickyTool{}}, []core.ToolCall{{ID: "c1", Name: "panic"}})
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	var got core.ToolResult
	for _, ev := range f.events {
		if r, ok := ev.(core.ToolResult); ok && r.ID == "c1" {
			got = r
		}
	}
	if got.Err == nil || !strings.Contains(got.Err.Error(), "tool panic") {
		t.Fatalf("want a tool-panic error surfaced, got %v", got.Err)
	}
	if strings.TrimSpace(got.Content) == "" {
		t.Fatalf("the error text must be model-visible in the result content")
	}
}

type panickyTool struct{}

func (panickyTool) Name() string            { return "panic" }
func (panickyTool) Description() string     { return "panics" }
func (panickyTool) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (panickyTool) Exec(context.Context, json.RawMessage) (string, error) {
	panic("boom")
}

func fileCall(t *testing.T, id, name string, args map[string]any) core.ToolCall {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return core.ToolCall{ID: id, Name: name, Args: raw}
}

func TestBatchEditsToOneFileRunInCallOrderAgainstThePreviousLanding(t *testing.T) {
	dir := realTemp(t)
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := []core.ToolCall{
		fileCall(t, "c1", "read", map[string]any{"path": path}),
		fileCall(t, "c2", "edit", map[string]any{"path": path, "old": "alpha", "new": "beta"}),
		fileCall(t, "c3", "edit", map[string]any{"path": path, "old": "beta", "new": "gamma"}),
	}
	k, f, s := batchKernel(t, []core.Tool{file.NewRead(), file.NewEdit()}, calls, rig.WithPriority(writesFirst))
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := resultOrder(f); !same(got, []string{"c1", "c2", "c3"}) {
		t.Fatalf("result order %v, want call order", got)
	}
	for _, ev := range f.events {
		r, ok := ev.(core.ToolResult)
		if !ok || r.ID == "c1" {
			continue
		}
		if r.Err != nil {
			t.Fatalf("%s refused: %v\n%s", r.ID, r.Err, r.Content)
		}
		if strings.Contains(r.Content, "changed since the read") {
			t.Fatalf("%s saw the pre-edit observation, so the landing did not re-arm the drift check:\n%s", r.ID, r.Content)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "gamma\n" {
		t.Fatalf("content = %q, want c3 applied to what c2 left", data)
	}
	sum := sha256.Sum256(data)
	if got := s.Files[path].Hash; got != hex.EncodeToString(sum[:]) {
		t.Fatalf("the recorded observation is %q, want the last landing's hash", got)
	}
}

type stateStore struct {
	mu   sync.Mutex
	vals map[string]string
}

func newStateStore() *stateStore {
	return &stateStore{vals: map[string]string{}}
}

type stateTool struct {
	name  string
	store *stateStore
}

func newStateTool(name string, store *stateStore) *stateTool {
	return &stateTool{name: name, store: store}
}

func (t *stateTool) Name() string            { return t.name }
func (t *stateTool) Description() string     { return "stateful test tool" }
func (t *stateTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (t *stateTool) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID   string `json:"id"`
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(args, &a)
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	if a.Mode == "write" {
		t.store.vals[a.ID] = "after-" + a.ID
		return "wrote " + a.ID, nil
	}
	if v, ok := t.store.vals[a.ID]; ok {
		return v, nil
	}
	return "before-" + a.ID, nil
}

func stateCall(callID, key, name, mode string) core.ToolCall {
	return core.ToolCall{ID: callID, Name: name, Args: json.RawMessage(`{"id":"` + key + `","mode":"` + mode + `"}`)}
}

func writesFirst(c core.ToolCall) int {
	if c.Name == "edit" || c.Name == "write" || strings.Contains(string(c.Args), `"write"`) || strings.Contains(string(c.Args), `"create"`) {
		return rig.PriorityWrite
	}
	return rig.PriorityRead
}

func resultContent(f *recorderFrontend, id string) string {
	for _, ev := range f.events {
		if r, ok := ev.(core.ToolResult); ok && r.ID == id {
			return r.Content
		}
	}
	return ""
}

func TestReadSeesTheTreeAfterTheWritesOfItsMessage(t *testing.T) {
	store := newStateStore()
	read, edit := newStateTool("read", store), newStateTool("edit", store)
	calls := []core.ToolCall{stateCall("c1", "a", "read", "read"), stateCall("c2", "a", "edit", "write")}
	k, f, s := batchKernel(t, []core.Tool{read, edit}, calls, rig.WithPriority(writesFirst))
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := resultContent(f, "c1"); got != "after-a" {
		t.Fatalf("the read must observe the edit's outcome: %q", got)
	}
	if got := resultOrder(f); !same(got, []string{"c1", "c2"}) {
		t.Fatalf("ToolResult order %v, want call order", got)
	}
	if got := toolOrder(s); !same(got, []string{"c1", "c2"}) {
		t.Fatalf("transcript order %v, want call order", got)
	}
}

func TestTheRuleHoldsWhenTheReadIsWrittenAfterTheEdit(t *testing.T) {
	store := newStateStore()
	read, edit := newStateTool("read", store), newStateTool("edit", store)
	calls := []core.ToolCall{stateCall("c1", "a", "edit", "write"), stateCall("c2", "b", "read", "read")}
	k, f, _ := batchKernel(t, []core.Tool{read, edit}, calls, rig.WithPriority(writesFirst))
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := resultContent(f, "c2"); got != "before-b" {
		t.Fatalf("the read after the edit reads its own key: %q", got)
	}
	if got := resultOrder(f); !same(got, []string{"c1", "c2"}) {
		t.Fatalf("ToolResult order %v, want call order", got)
	}
}

func TestTheDelegateStartsAfterTheEdit(t *testing.T) {
	edit, delegate := newTimed("edit"), newTimed("delegate")
	calls := []core.ToolCall{timedCall("c1", "edit", 60), timedCall("c2", "delegate", 10)}
	k, _, _ := batchKernel(t, []core.Tool{edit, delegate}, calls, rig.WithPriority(writesFirst))
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if delegate.starts["c2"].Before(edit.ends["c1"]) {
		t.Fatal("the worker started before the edit finished")
	}
}

func TestTodoReadShowsTheTaskTheCreateMade(t *testing.T) {
	todo := newStateTool("todo", newStateStore())
	calls := []core.ToolCall{stateCall("c1", "t1", "todo", "write"), stateCall("c2", "t1", "todo", "read")}
	k, f, _ := batchKernel(t, []core.Tool{todo}, calls, rig.WithPriority(writesFirst))
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := resultContent(f, "c2"); got != "after-t1" {
		t.Fatalf("the read must show the task the create made: %q", got)
	}
}

func TestWritesRunInIndexOrderAndAlone(t *testing.T) {
	edit := newTimed("edit")
	calls := []core.ToolCall{timedCall("cb", "edit", 60), timedCall("ca", "edit", 10)}
	k, f, _ := batchKernel(t, []core.Tool{edit}, calls, rig.WithPriority(writesFirst))
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if edit.starts["ca"].Before(edit.ends["cb"]) {
		t.Fatal("the writes must run in index order: ca started before cb finished")
	}
	if edit.peak.Load() != 1 {
		t.Fatalf("peak %d: the writes must not overlap", edit.peak.Load())
	}
	if got := resultOrder(f); !same(got, []string{"cb", "ca"}) {
		t.Fatalf("ToolResult order %v, want call order", got)
	}
}

func TestAFailedWriteDoesNotStopTheOnesAfterIt(t *testing.T) {
	edit := newTimed("edit")
	edit.fails = map[string]bool{"c1": true}
	calls := []core.ToolCall{timedCall("c1", "edit", 10), timedCall("c2", "edit", 10)}
	k, _, _ := batchKernel(t, []core.Tool{edit}, calls, rig.WithPriority(writesFirst))
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if edit.starts["c2"].IsZero() {
		t.Fatal("the write after the failed one must still run")
	}
}

func TestInterruptMidDrainCancelsTheRest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	edit, read := newTimed("edit"), newTimed("read")
	edit.cancel = cancel
	calls := []core.ToolCall{timedCall("c1", "edit", 30), timedCall("c2", "edit", 30), timedCall("c3", "read", 30)}
	evs := []evt.Signal{callEv(calls[0]), callEv(calls[1]), callEv(calls[2]), doneEv()}
	p := &scriptedProvider{turns: []scriptedTurn{{events: evs}, {holdCtx: true}}}
	f := &recorderFrontend{inputs: make(chan string, 1)}
	s := core.NewSession()
	k := rig.New(rig.WithProvider(p), rig.WithFrontend(f), rig.WithPolicy(&transcriptPolicy{}), rig.WithTools(edit, read), rig.WithPriority(writesFirst), rig.WithSession(s))
	f.inputs <- "go"
	close(f.inputs)
	if err := loop.Run(ctx, k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if d := edit.ends["c2"].Sub(edit.starts["c2"]); d >= 30*time.Millisecond {
		t.Fatalf("the write not yet started must not do its work: %v", d)
	}
	if d := read.ends["c3"].Sub(read.starts["c3"]); d >= 30*time.Millisecond {
		t.Fatalf("the reads must never do their work: %v", d)
	}
	if got := toolOrder(s); !same(got, []string{"c1", "c2", "c3"}) {
		t.Fatalf("the batch must drain into the transcript in order, got %v", got)
	}
}

func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}
