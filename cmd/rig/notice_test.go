package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/frontend/oneshot"
	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

type captureFrontend struct{ events []core.Event }

func (c *captureFrontend) Input(ctx context.Context) (string, error) { return "", context.Canceled }
func (c *captureFrontend) Notify(ev core.Event)                      { c.events = append(c.events, ev) }

func TestANoticeReachesTheFrontendAndNeverStderrWhileItOwnsTheScreen(t *testing.T) {
	fe := &captureFrontend{}
	var stderr bytes.Buffer
	r := &root{fe: fe, errOut: &stderr}
	r.notice("decision", "review: fire: refused")
	if len(fe.events) != 1 {
		t.Fatalf("events = %v, want one notice", fe.events)
	}
	n, ok := fe.events[0].(core.Notice)
	if !ok || n.Source != "decision" || n.Text != "review: fire: refused" {
		t.Fatalf("event = %#v, want the notice with its source and text", fe.events[0])
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr got %q, want nothing while a frontend owns the screen", stderr.String())
	}
}

func TestANoticeInAHeadlessRunIsOneStderrLine(t *testing.T) {
	var errOut bytes.Buffer
	r := &root{fe: &oneshot.OneShot{Err: &errOut}, errOut: &errOut}
	r.notice("decision", "queue full, dropping the bash proposal")
	if got := errOut.String(); !strings.Contains(got, "rig: decision: queue full, dropping the bash proposal") || strings.Count(got, "rig:") != 1 {
		t.Fatalf("headless notice = %q, want exactly one rig: line", got)
	}
}

func TestANoticeWithNoFrontendYetGoesToStderr(t *testing.T) {
	var errOut bytes.Buffer
	r := &root{errOut: &errOut}
	r.notice("decision", "early")
	if got := errOut.String(); got != "rig: decision: early\n" {
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
