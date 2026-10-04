package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2"
	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/frontend/oneshot"
	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

func awaitText(t *testing.T, buf *lockedBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("stderr = %q, want %q", buf.String(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestANoticeReachesTheFrontendAndNeverStderrWhileItOwnsTheScreen(t *testing.T) {
	stderr := &lockedBuffer{}
	fe := &recordingFrontend{}
	r := testRoot(fe)
	r.errOut = stderr
	storeRoot(t, r)
	fleet(r)
	go r.engine.Start(context.Background())
	defer r.engine.Stop()
	r.rec = recorderFor(r, fe)
	broadcast.Say(r.room.Add(rig.MemberDecision), "decision", "review: fire: refused")
	awaitCount(t, fe, 1)
	n, ok := fe.events[0].(core.Notice)
	if !ok || n.Source != "decision" || n.Text != "review: fire: refused" {
		t.Fatalf("event = %#v, want the notice with its source and text", fe.events[0])
	}
	if stderr.String() != "" {
		t.Fatalf("stderr got %q, want nothing while a frontend owns the screen", stderr.String())
	}
}

func TestANoticeInAHeadlessRunIsOneStderrLine(t *testing.T) {
	errOut := &lockedBuffer{}
	r := testRoot(&oneshot.OneShot{Err: errOut})
	r.errOut = errOut
	storeRoot(t, r)
	fleet(r)
	go r.engine.Start(context.Background())
	defer r.engine.Stop()
	r.rec = recorderFor(r, &oneshot.OneShot{Err: errOut})
	broadcast.Say(r.room.Add(rig.MemberDecision), "decision", "queue full, dropping the bash proposal")
	awaitText(t, errOut, "rig: decision: queue full, dropping the bash proposal")
	if got := errOut.String(); strings.Count(got, "rig:") != 1 {
		t.Fatalf("headless notice = %q, want exactly one rig: line", got)
	}
}

func TestANoticeWithNoRecorderYetGoesToStderr(t *testing.T) {
	errOut := &lockedBuffer{}
	r := testRoot(nullFrontend{})
	r.rec = nil
	r.errOut = errOut
	fleet(r)
	go r.engine.Start(context.Background())
	defer r.engine.Stop()
	broadcast.Say(r.room.Add(rig.MemberGraph), "graph", "early")
	awaitText(t, errOut, "rig: graph: early\n")
	if got := errOut.String(); got != "rig: graph: early\n" {
		t.Fatalf("got %q", got)
	}
}

func TestTheReviewFireNamesNoModelAndFallsBackToTheSessionsOwn(t *testing.T) {
	var seen sched.DelegateInput
	r := &root{activeID: "ox-alpha", cwd: t.TempDir()}
	r.delegate = func(in sched.DelegateInput) (sched.DelegateResult, error) {
		seen = in
		return sched.DelegateResult{Model: "ox-alpha", Exit: 0, Stdout: "verdict: 1 approve\n"}, nil
	}
	fire := r.reviewFire(t.TempDir(), store.DB{}, "http://127.0.0.1:1", "rig", t.TempDir(), "", nil)
	reply, model, err := fire(context.Background(), "review these")
	if err != nil {
		t.Fatal(err)
	}
	if seen.Model != "" {
		t.Fatalf("the fire named %q; it must name no model so the resident one resolves", seen.Model)
	}
	if seen.DefaultModel != "ox-alpha" {
		t.Fatalf("the fallback = %q, want the session's active model", seen.DefaultModel)
	}
	if !seen.NoTools {
		t.Fatal("the review fire runs with no tools")
	}
	if model != "ox-alpha" || reply != "verdict: 1 approve\n" {
		t.Fatalf("fire returned %q %q, want the delegate's model and stdout", model, reply)
	}
	r.activeID = "dsv4"
	fire(context.Background(), "again")
	if seen.DefaultModel != "dsv4" {
		t.Fatalf("the fallback must follow the session's model at fire time, got %q", seen.DefaultModel)
	}
}
