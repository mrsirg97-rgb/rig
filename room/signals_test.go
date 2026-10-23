package room

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
)

func signalCase(s evt.Signal) string {
	switch s.(type) {
	case StartChild:
		return "start"
	case StopChild:
		return "stop"
	case SwapChild:
		return "swap"
	case TableChange:
		return "table"
	case Update:
		return "update"
	case JobStatus:
		return "job"
	}
	return ""
}

func TestTheFleetSignalsDoNotCrossTheirCases(t *testing.T) {
	cases := []struct {
		what   string
		signal evt.Signal
		want   string
	}{
		{"start", NewStartChild("m", 1, "c", nil, ""), "start"},
		{"stop", NewStopChild("m", 1), "stop"},
		{"swap", NewSwapChild(SwapChildInput{Model: "m", Out: "o", Grace: 1}), "swap"},
		{"table", NewTableChange(json.RawMessage(`{}`)), "table"},
		{"update", NewUpdate("3.4.1"), "update"},
		{"job", NewJobStatus(JobStatusInput{Job: "j1"}), "job"},
	}
	for _, c := range cases {
		if got := signalCase(c.signal); got != c.want {
			t.Fatalf("%s arrives as %q, want %q (the getter sets overlap; each signal carries its own marker)", c.what, got, c.want)
		}
	}
}

func TestTheFleetWireKeepsItsBytes(t *testing.T) {
	started := time.Date(2026, 10, 23, 21, 0, 0, 0, time.UTC)
	cases := []struct {
		what   string
		signal evt.Signal
		want   string
	}{
		{"start", NewStartChild("m", 7, "cmd", []string{"A=1"}, "/l"), `{"Model":"m","Port":7,"Command":"cmd","Env":["A=1"],"Log":"/l"}`},
		{"stop", NewStopChild("m", 2), `{"Model":"m","Grace":2}`},
		{"swap", NewSwapChild(SwapChildInput{Model: "m", Out: "o", Port: 7, Command: "cmd", Env: []string{"A=1"}, Log: "/l", Grace: 2, Forced: true}), `{"Model":"m","Out":"o","Port":7,"Command":"cmd","Env":["A=1"],"Log":"/l","Grace":2,"Forced":true}`},
		{"table", NewTableChange(json.RawMessage(`{"id":"m"}`)), `{"Row":{"id":"m"}}`},
		{"update", NewUpdate("3.4.1"), `{"Version":"3.4.1"}`},
		{"job", NewJobStatus(JobStatusInput{Job: "j1", Prompt: "p", State: JobRunning, Started: started, Workers: 2, Call: "bash", CallAt: started}), `{"Job":"j1","Prompt":"p","State":"running","Started":"2026-10-23T21:00:00Z","Ended":"0001-01-01T00:00:00Z","Reason":"","Workers":2,"Call":"bash","CallAt":"2026-10-23T21:00:00Z"}`},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.signal)
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if string(got) != c.want {
			t.Fatalf("%s wire = %s, want %s", c.what, got, c.want)
		}
	}
}

func TestParseJobStatusBuildsTheSameValue(t *testing.T) {
	started := time.Date(2026, 10, 23, 21, 0, 0, 0, time.UTC)
	want := NewJobStatus(JobStatusInput{Job: "j1", Prompt: "p", State: JobDone, Started: started, Ended: started, Summary: "s", Workers: 1})
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseJobStatus(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(raw) {
		t.Fatalf("the decoded status marshals %s, want %s", again, raw)
	}
}
