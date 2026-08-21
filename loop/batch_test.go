package loop_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/loop"
)

// timedTool sleeps for its args' ms, records when each call started and
// ended, and tracks the peak number of calls in flight — the batch's
// observable: what overlapped, and what waited.
type timedTool struct {
	name   string
	cur    atomic.Int32
	peak   atomic.Int32
	mu     sync.Mutex
	starts map[string]time.Time
	ends   map[string]time.Time
	cancel context.CancelFunc
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

func batchKernel(t *testing.T, tools []core.Tool, calls []core.ToolCall, opts ...rig.Option) (*rig.Kernel, *recorderFrontend, *core.Session) {
	t.Helper()
	evs := make([]core.Event, 0, len(calls)+1)
	for _, c := range calls {
		evs = append(evs, callEv(c))
	}
	evs = append(evs, doneEv())
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: evs},
		{events: []core.Event{textEv("done"), doneEv()}},
	}}
	f := &recorderFrontend{inputs: make(chan string, 1)}
	s := core.NewSession()
	base := []rig.Option{rig.WithProvider(p), rig.WithFrontend(f), rig.WithPolicy(&transcriptPolicy{}), rig.WithTools(tools...)}
	k := rig.New(append(base, opts...)...)
	k.Session = s
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

var onlyRead = func(c core.ToolCall) bool { return c.Name == "read" }

// SPEC_EVT 2a: concurrent-eligible calls overlap; results are emitted
// and appended in call order regardless of completion order; each
// result's Duration is its own.
func TestBatchRunsReadsConcurrentlyAndEmitsInCallOrder(t *testing.T) {
	read := newTimed("read")
	calls := []core.ToolCall{timedCall("c1", "read", 90), timedCall("c2", "read", 40), timedCall("c3", "read", 10)}
	k, f, s := batchKernel(t, []core.Tool{read}, calls, rig.WithConcurrent(onlyRead))
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

// A call the predicate refuses is a barrier: everything before it
// finishes first, it runs alone, and the calls after it wait for it.
func TestBatchMutatingCallIsABarrier(t *testing.T) {
	read, write := newTimed("read"), newTimed("write")
	calls := []core.ToolCall{timedCall("c1", "read", 60), timedCall("c2", "read", 20), timedCall("c3", "write", 10), timedCall("c4", "read", 10)}
	k, f, s := batchKernel(t, []core.Tool{read, write}, calls, rig.WithConcurrent(onlyRead))
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
	if write.starts["c3"].Before(read.ends["c1"]) || write.starts["c3"].Before(read.ends["c2"]) {
		t.Fatal("the write started before the reads ahead of it finished")
	}
	if read.starts["c4"].Before(write.ends["c3"]) {
		t.Fatal("the read after the write started before the write finished")
	}
}

// No predicate is the sequential loop, byte-identical: nothing overlaps.
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

// Parallel bounds a run: four eligible calls, at most two in flight.
func TestBatchParallelBoundsTheRun(t *testing.T) {
	read := newTimed("read")
	calls := []core.ToolCall{timedCall("c1", "read", 40), timedCall("c2", "read", 40), timedCall("c3", "read", 40), timedCall("c4", "read", 40)}
	k, f, _ := batchKernel(t, []core.Tool{read}, calls, rig.WithConcurrent(onlyRead), rig.WithParallel(2))
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

// A run-context cancel landing inside a concurrent run: every call's
// result still lands (the batch drains), the transcript keeps the
// batch whole, and the run exits clean at the next boundary.
func TestBatchCancellationMidRunDrainsTheBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	read := newTimed("read")
	read.cancel = cancel
	calls := []core.ToolCall{timedCall("c1", "read", 30), timedCall("c2", "read", 30)}
	evs := []core.Event{callEv(calls[0]), callEv(calls[1]), doneEv()}
	p := &scriptedProvider{turns: []scriptedTurn{{events: evs}, {holdCtx: true}}}
	f := &recorderFrontend{inputs: make(chan string, 1)}
	s := core.NewSession()
	k := rig.New(rig.WithProvider(p), rig.WithFrontend(f), rig.WithPolicy(&transcriptPolicy{}), rig.WithTools(read), rig.WithConcurrent(onlyRead))
	k.Session = s
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

// The guard under a batch: identical failing calls in one concurrent
// run are counted without a race, and the bound still holds for the
// next turn's identical call.
func TestBatchGuardCountsConcurrentFailures(t *testing.T) {
	calls := make([]core.ToolCall, 0, 6)
	for i := 0; i < 6; i++ {
		calls = append(calls, core.ToolCall{ID: "c" + itoa(i+1), Name: "read", Args: json.RawMessage(`{"same":true}`)})
	}
	failing := &failTool{name: "read"}
	counted := &countingGuard{}
	k, f, _ := batchKernel(t, []core.Tool{failing}, calls, rig.WithConcurrent(onlyRead), rig.WithMiddleware(counted))
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
