package swarm_test

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v2/swarm"
)

type recordFrontend struct {
	mu     sync.Mutex
	events []core.Event
}

func (r *recordFrontend) Input(ctx context.Context) (string, error) { return "", io.EOF }

func (r *recordFrontend) Notify(ev core.Event) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

func (r *recordFrontend) notices() []core.SwarmNotice {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []core.SwarmNotice
	for _, ev := range r.events {
		if n, ok := ev.(core.SwarmNotice); ok {
			out = append(out, n)
		}
	}
	return out
}

func (r *recordFrontend) statuses() []core.SwarmStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []core.SwarmStatus
	for _, ev := range r.events {
		if s, ok := ev.(core.SwarmStatus); ok {
			out = append(out, s)
		}
	}
	return out
}

func noticeTexts(t *testing.T, fe *recordFrontend) []string {
	t.Helper()
	ns := fe.notices()
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Text
	}
	return out
}

func waitForNotices(t *testing.T, fe *recordFrontend, n int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ns := fe.notices()
		if len(ns) >= n {
			return noticeTexts(t, fe)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d notices, have %d:\n%v", n, len(ns), noticeTexts(t, fe))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSwarmNoticesTaskFailed(t *testing.T) {
	h := newHarness(t)
	h.create(t, "do the work")
	h.spawn.queue = []sched.SpawnResult{{Exit: 1}, {Exit: 1}}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	got := waitForNotices(t, h.fe, 3)
	want := []string{
		"swarm: w1 died — t1 restarted",
		"swarm: w1 died — t1 exited",
		"swarm: t1 failed — the worker died twice",
	}
	if len(got) != len(want) {
		t.Fatalf("notices = %d, want %d:\n%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("notice %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSwarmNoticesReviewerRejected(t *testing.T) {
	h := newHarness(t)
	h.create(t, "ship the feature")
	h.spawn.queue = []sched.SpawnResult{
		{Exit: 0, Stdout: "done\n"},
		{Exit: 0, Stdout: "reviewed\nverdict: reject tests are missing\n"},
		{Exit: 0, Stdout: "done\n"},
		{Exit: 0, Stdout: "looks good\nverdict: accept\n"},
	}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.start(t, swarm.StartOpts{Count: 1, Role: "reviewer"})
	got := waitForNotices(t, h.fe, 1)
	if got[0] != "swarm: t1 rejected — tests are missing" {
		t.Fatalf("reject notice = %q", got[0])
	}
	h.waitFor(t, "the rejection picked up and accepted", func() bool {
		return h.status(t, "t1") == "done"
	})
}

func TestSwarmNoticesStop(t *testing.T) {
	h := newHarness(t)
	h.start(t, swarm.StartOpts{Count: 2, Role: "worker"})
	if _, err := h.ctl.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	got := waitForNotices(t, h.fe, 1)
	if got[0] != "swarm: /swarm exited — 2 workers stopped" {
		t.Fatalf("stop notice = %q", got[0])
	}
}

func TestSwarmStatusFramesClaimHeartbeatAndFinish(t *testing.T) {
	h := newHarness(t)
	h.create(t, "do the work")
	h.spawn.onCall = func(observe func([]byte)) {
		for i := 0; i < 40; i++ {
			observe([]byte("rig: heartbeat\n"))
		}
	}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.waitFor(t, "the task in review", func() bool {
		return h.status(t, "t1") == "review"
	})
	h.waitFor(t, "the finished frame", func() bool {
		st := h.fe.statuses()
		return len(st) > 0 && st[len(st)-1].Workers[0].Task == ""
	})
	st := h.fe.statuses()
	if len(st) < 2 {
		t.Fatalf("statuses = %d, want the claim and the finish", len(st))
	}
	if st[0].Workers[0].Task != "t1" || st[0].Workers[0].State != "running" {
		t.Fatalf("the first status is not the claim: %+v", st[0])
	}
	last := st[len(st)-1]
	if last.Workers[0].Done != 1 || last.Workers[0].Task != "" {
		t.Fatalf("the last status is not the finish: %+v", last)
	}
	if last.Workers[0].Heartbeat.IsZero() {
		t.Fatalf("the bytes never updated the heartbeat: %+v", last)
	}
	if last.Pending != 0 || last.Review != 1 {
		t.Fatalf("the finish status does not carry the fold counts: %+v", last)
	}
	if _, err := h.ctl.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	h.waitFor(t, "the forced stop frame", func() bool {
		st := h.fe.statuses()
		return len(st) > 0 && len(st[len(st)-1].Workers) == 0
	})
}
