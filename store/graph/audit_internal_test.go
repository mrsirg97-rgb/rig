package graph

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/decision"
	"github.com/mrsirg97-rgb/rig/v3/store"
	"github.com/mrsirg97-rgb/rig/v3/store/scope"
)

func TestStopWaitsOnADeadClient(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &lspClient{cmd: cmd, stdin: stdin, lang: "dead", pending: map[int64]chan json.RawMessage{}, stderr: newBoundedTail(lspStderrTail)}
	go c.readLoop(&emptyReader{})
	deadline := time.After(2 * time.Second)
	for c.alive() {
		select {
		case <-deadline:
			t.Fatal("the client never died")
		case <-time.After(time.Millisecond):
		}
	}
	c.stop()
	if cmd.ProcessState == nil {
		t.Fatal("a dead client's process was never waited: one zombie per death")
	}
	c.stop()
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

func TestASecondStopJoinsTheReaperThatGotThereFirst(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exec 1>&-; sleep 0.25")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &lspClient{cmd: cmd, stdin: stdin, lang: "dead", pending: map[int64]chan json.RawMessage{}, stderr: newBoundedTail(lspStderrTail)}
	go c.readLoop(stdout)
	deadline := time.After(2 * time.Second)
	for c.alive() {
		select {
		case <-deadline:
			t.Fatal("the client never died")
		case <-time.After(time.Millisecond):
		}
	}
	c.stop()
	if cmd.ProcessState == nil {
		t.Fatal("stop returned while the reaper was still inside Wait: one zombie per death")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("the child was reaped by the client, exit %d", code)
	}
}

func TestABodyOverTheReadCeilingDiesTheClient(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf 'Content-Length: 99999999\\r\\n\\r\\n'; sleep 5")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &lspClient{cmd: cmd, stdin: stdin, lang: "huge", pending: map[int64]chan json.RawMessage{}, stderr: newBoundedTail(lspStderrTail)}
	go c.readLoop(stdout)
	deadline := time.After(2 * time.Second)
	for c.alive() {
		select {
		case <-deadline:
			c.stop()
			t.Fatal("a Content-Length over the read ceiling must die the client, not allocate its word")
		case <-time.After(time.Millisecond):
		}
	}
	c.stop()
}

func TestADyingServerNamesItsStderr(t *testing.T) {
	engine := evt.NewEngine()
	go engine.Start(context.Background())
	defer engine.Stop()
	room := broadcast.NewRoom("test", func(id int64) broadcast.Transport {
		return broadcast.NewLoopTransport(id, engine, 0)
	})
	said := make(chan string, 8)
	room.Add(-1).Subscribe(context.Background(), func(err error, messages ...broadcast.Message) {
		for _, m := range messages {
			if n, ok := m.Signal().(evt.Notice); err == nil && ok {
				said <- n.Text
			}
		}
	})
	_, err := startServer(context.Background(), "boom", []string{"sh", "-c", "echo the server exploded >&2; exit 7"}, room.Add(0))
	if err == nil {
		t.Fatal("a server that dies at initialize must refuse")
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case line := <-said:
			if strings.Contains(line, "exploded") {
				return
			}
		case <-deadline:
			t.Fatal("the death line never named the server's stderr")
		}
	}
}

func TestLspURIEscapesTheReservedGrammar(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x#y?q z.ts")
	uri := lspURI(p)
	if tail := strings.TrimPrefix(uri, "file://"); strings.ContainsAny(tail, "#?") {
		t.Fatalf("lspURI(%q) = %q: a fragment or query leaks into the path", p, uri)
	}
	got, err := lspPath(uri)
	if err != nil {
		t.Fatalf("lspPath(%q): %v", uri, err)
	}
	if got != p {
		t.Fatalf("the uri must round-trip: %q != %q", got, p)
	}
}

func TestTheReadCeilingNeverSplitsARune(t *testing.T) {
	ascii := strings.Repeat("a", ReadCeiling)
	if got := truncateAt(ascii, ReadCeiling); len(got) != ReadCeiling {
		t.Fatalf("ascii truncation len = %d, want the ceiling exact", len(got))
	}
	straddle := strings.Repeat("a", ReadCeiling-2) + "日日日"
	got := truncateAt(straddle, ReadCeiling)
	if !utf8.ValidString(got) {
		t.Fatalf("the truncated pack is not valid UTF-8: len %d", len(got))
	}
	if len(got) > ReadCeiling {
		t.Fatalf("truncated len %d over the ceiling", len(got))
	}
	if got != strings.Repeat("a", ReadCeiling-2) {
		t.Fatalf("the truncation must fall back to the last whole rune, got len %d", len(got))
	}
}

func TestCoverageRefusesADeadStore(t *testing.T) {
	db, err := Open(t.TempDir(), "k", "w")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := coverage(context.Background(), db, t.TempDir(), &b); err == nil {
		t.Fatal("coverage must refuse a store it cannot read, not report a lie")
	}
}

func twoSymbolProject(t *testing.T) (root, home string, db store.DB) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package alpha\n\nfunc Target() int { return Helper() }\n\nfunc Helper() int { return 1 }\n"
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	home = t.TempDir()
	seed := NewQueue(home, nil)
	if _, err := seed.IndexProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	db, err := Open(home, scope.Key(root), scope.Worktree(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return root, home, db
}

func TestTheExactFitCandidateLoads(t *testing.T) {
	root, home, db := twoSymbolProject(t)
	ctx := context.Background()
	task := "find the target helper"
	cands, err := lexicalCandidates(ctx, db, task, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) < 2 {
		t.Fatalf("the fixture must offer two candidates, got %d", len(cands))
	}
	first, err := candidateItem(root, cands[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := candidateItem(root, cands[1])
	if err != nil {
		t.Fatal(err)
	}
	exact := len(first) + len(second)
	q := NewQueue(home, nil, WithPackCaps(exact, ReadCeiling))
	out, err := q.packTask(ctx, db, root, task)
	if err != nil {
		t.Fatalf("packTask: %v", err)
	}
	if !strings.Contains(out, "Target") || !strings.Contains(out, "Helper") {
		t.Fatalf("a candidate that exactly fills the item ceiling must load, got:\n%s", out)
	}
}

type shortScorer struct {
	by int
}

func (s shortScorer) Score(_ context.Context, _ string, items []string) ([]decision.PackVerdict, error) {
	n := len(items) + s.by
	if n < 0 {
		n = 0
	}
	return make([]decision.PackVerdict, n), nil
}

func TestPackTaskRefusesAMismatchedVerdictCount(t *testing.T) {
	root, home, db := twoSymbolProject(t)
	ctx := context.Background()
	for _, by := range []int{1, -1} {
		q := NewQueue(home, nil, WithScorer(shortScorer{by: by}))
		_, err := q.packTask(ctx, db, root, "find the target helper")
		if err == nil {
			t.Fatalf("a scorer answering %d verdicts for a different candidate count must refuse", by)
		}
		if !strings.Contains(err.Error(), "verdicts") || !strings.Contains(err.Error(), "candidates") {
			t.Fatalf("the refusal names both counts, got %v", err)
		}
	}
	q := NewQueue(home, nil, WithScorer(shortScorer{}))
	if _, err := q.packTask(ctx, db, root, "find the target helper"); err != nil {
		t.Fatalf("a matched scorer packs: %v", err)
	}
}
