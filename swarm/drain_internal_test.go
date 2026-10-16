package swarm

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/store"
	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
	todostore "github.com/mrsirg97-rgb/rig/v3/store/todo"
)

type loopFixture struct {
	c      *Controller
	engine evt.Engine
	todoDB store.DB
	proj   todostore.Project
}

func newLoopController(t *testing.T) *loopFixture {
	t.Helper()
	f := &loopFixture{}
	f.engine = evt.NewEngine()
	go f.engine.Start(context.Background())
	t.Cleanup(f.engine.Stop)
	todoDB, _, _, err := store.Open(filepath.Join(t.TempDir(), "todo.sqlite"), todostore.Statements(), todostore.SchemaVersion)
	if err != nil {
		t.Fatalf("todo store: %v", err)
	}
	f.todoDB = todoDB
	f.proj = todostore.ProjectOf(t.TempDir())
	f.c = New(Opts{
		TodoDB:    todoDB,
		Project:   func(context.Context, string) (todostore.Project, error) { return f.proj, nil },
		WorkerCmd: []string{"/x/rig"},
		Cwd:       f.proj.Dir,
		RigHome:   t.TempDir(),
		StateDir:  t.TempDir(),
		Sandbox:   "off",
		Delegate:  func(sched.DelegateInput) (sched.DelegateResult, error) { return sched.DelegateResult{Exit: 0}, nil },
		Engine:    f.engine,
		Room: broadcast.NewRoom("fleet", func(origin int64) broadcast.Transport {
			return broadcast.NewLoopTransport(origin, f.engine, rig.PriorityFleet)
		}),
	})
	t.Cleanup(func() { f.c.Stop() })
	return f
}

func (f *loopFixture) start(t *testing.T) (cw, cr *worker) {
	t.Helper()
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := f.c.Start(ctx, StartOpts{Count: 1, Role: RoleWorker}); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	if _, err := f.c.Start(ctx, StartOpts{Count: 1, Role: RoleReviewer}); err != nil {
		t.Fatalf("start reviewer: %v", err)
	}
	f.c.call(func() {})
	return f.c.workers[0], f.c.workers[1]
}

func (f *loopFixture) budgets(t *testing.T) (int, int) {
	t.Helper()
	done := make(chan struct{})
	var retries, rejects int
	f.c.call(func() { retries, rejects = len(f.c.retries), len(f.c.rejects); close(done) })
	<-done
	return retries, rejects
}

func (f *loopFixture) status(t *testing.T, id string) string {
	t.Helper()
	rows, err := f.todoDB.DB.Query(`SELECT status FROM tasks WHERE scope = ? AND id = ?`, f.proj.Key, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		return ""
	}
	var status string
	if err := rows.Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func (f *loopFixture) toReview(t *testing.T, cw *worker, id string) {
	t.Helper()
	if _, err := todostore.Claim(context.Background(), f.todoDB, f.proj, cw.identity, ""); err != nil {
		t.Fatalf("claim %s: %v", id, err)
	}
	if _, err := todostore.Complete(context.Background(), f.todoDB, f.proj, id, cw.identity, true); err != nil {
		t.Fatalf("complete %s: %v", id, err)
	}
}

func (f *loopFixture) settleOnce(cw *worker, id string) {
	f.c.call(func() { f.c.settle(cw, id, workResult{}) })
}

func (f *loopFixture) acceptVerdict(t *testing.T, cr *worker, id string) {
	t.Helper()
	cr.member.Publish(context.Background(), func(error) {}, broadcast.NewMessage(int64(cr.id), true, core.Verdict{Accept: true, Reason: "fine"}))
	deadline := 100
	for {
		var stamped bool
		f.c.call(func() { stamped = cr.verdict != nil })
		if stamped {
			return
		}
		deadline--
		if deadline == 0 {
			t.Fatalf("the reviewer's verdict never landed")
		}
	}
}

func TestSettleRetiresTheBudgetsWhenTheSwarmFailsTheTask(t *testing.T) {
	f := newLoopController(t)
	cw, _ := f.start(t)
	if _, err := todostore.Create(context.Background(), f.todoDB, f.proj, todostore.CreateItem{Text: "doomed"}, "sess-architect"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := todostore.Claim(context.Background(), f.todoDB, f.proj, cw.identity, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	f.settleOnce(cw, "t1")
	if _, err := todostore.Claim(context.Background(), f.todoDB, f.proj, cw.identity, ""); err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	f.settleOnce(cw, "t1")
	if got := f.status(t, "t1"); got != "failed" {
		t.Fatalf("status = %q, want failed (the task's terminal)", got)
	}
	if retries, rejects := f.budgets(t); retries+rejects != 0 {
		t.Fatalf("the budgets hold %d retries and %d rejects for the failed task, want none (the terminal retires them)", retries, rejects)
	}
}

func TestReviewerAcceptRetiresTheBudgetsAtTheVerdict(t *testing.T) {
	f := newLoopController(t)
	cw, cr := f.start(t)
	if _, err := todostore.Create(context.Background(), f.todoDB, f.proj, todostore.CreateItem{Text: "accepted late"}, "sess-architect"); err != nil {
		t.Fatalf("create: %v", err)
	}
	f.toReview(t, cw, "t1")
	f.settleOnce(cw, "t1")
	f.acceptVerdict(t, cr, "t1")
	ti, err := todostore.Task(context.Background(), f.todoDB, f.proj, "t1", cr.identity)
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if res := f.c.delegateWork(cr, "t1", ti); !res.ok {
		t.Fatalf("the accepted verdict did not land: %+v", res)
	}
	if got := f.status(t, "t1"); got != "done" {
		t.Fatalf("status = %q, want done (the verdict's terminal)", got)
	}
	if retries, rejects := f.budgets(t); retries+rejects != 0 {
		t.Fatalf("the budgets hold %d retries and %d rejects for the accepted task, want none (the terminal retires them)", retries, rejects)
	}
}

func (f *loopFixture) reviewerHold(t *testing.T, cr *worker, id string) {
	t.Helper()
	if _, err := todostore.Claim(context.Background(), f.todoDB, f.proj, cr.identity, "review"); err != nil {
		t.Fatalf("claim review %s: %v", id, err)
	}
}

func TestRejectionsRetireOnlyWhenTheSwarmFailsTheTask(t *testing.T) {
	f := newLoopController(t)
	cw, cr := f.start(t)
	if _, err := todostore.Create(context.Background(), f.todoDB, f.proj, todostore.CreateItem{Text: "rejected thrice"}, "sess-architect"); err != nil {
		t.Fatalf("create: %v", err)
	}
	f.toReview(t, cw, "t1")
	f.reviewerHold(t, cr, "t1")
	if err := f.c.rejectTask(cr, "t1", "not good enough"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if retries, rejects := f.budgets(t); retries != 0 || rejects != 1 {
		t.Fatalf("after one rejection the budgets hold %d retries and %d rejects, want the reject held (a rejection is not a terminal)", retries, rejects)
	}
	for i := 0; i < 2; i++ {
		f.toReview(t, cw, "t1")
		f.reviewerHold(t, cr, "t1")
		if err := f.c.rejectTask(cr, "t1", "not good enough"); err != nil {
			t.Fatalf("reject: %v", err)
		}
	}
	if got := f.status(t, "t1"); got != "failed" {
		t.Fatalf("status = %q, want failed (the third rejection fails the task)", got)
	}
	if retries, rejects := f.budgets(t); retries+rejects != 0 {
		t.Fatalf("the budgets hold %d retries and %d rejects for the failed task, want none (the terminal retires them)", retries, rejects)
	}
}
