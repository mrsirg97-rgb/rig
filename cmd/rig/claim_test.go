package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/models"
	machineroom "github.com/mrsirg97-rgb/rig/v3/room"
)

type refusing struct{}

func (refusing) Stream(context.Context, core.Request) (<-chan evt.Signal, error) {
	return nil, errors.New("429 overloaded")
}

type answering struct {
	port int
	seen chan bool
}

func (a answering) Stream(context.Context, core.Request) (<-chan evt.Signal, error) {
	a.seen <- machineroom.Answers(a.port)
	ch := make(chan evt.Signal, 1)
	ch <- core.Done{}
	close(ch)
	return ch, nil
}

func childRow(id string, port int) models.Model {
	return models.Model{ID: id, Window: 8192, MaxTokens: 2048, Reserve: 1024, KeepRecent: 2048, Role: models.RoleWorker, Command: "python3 -m http.server ${PORT}", Port: port, Places: 1, StopGrace: 1}
}

func roomOf(t *testing.T, rows ...models.Model) (string, *children, machineroom.Member) {
	t.Helper()
	home := t.TempDir()
	table, err := models.New(rows...)
	if err != nil {
		t.Fatal(err)
	}
	kids := newChildren(home, table, nil, nil)
	m, err := machineroom.New(home, kids.places, kids.effects, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(); kids.close() })
	return home, kids, m
}

func TestARemoteRowsFailureStartsNothing(t *testing.T) {
	home, kids, m := roomOf(t)
	c := claiming{inner: refusing{}, machine: m, kids: kids, key: machineroom.KeyOf("remote"), priority: machineroom.PriorityTurn, row: models.Model{ID: "remote"}}
	if _, err := c.Stream(context.Background(), core.Request{}); err == nil {
		t.Fatal("the provider's error must reach the caller")
	}
	await(t, "the claim to release", 5*time.Second, func() bool { return len(m.Holders(c.key)) == 0 })
	kids.stop("remote", 0)
	if _, err := os.Stat(filepath.Join(home, "logs", "remote.log")); err == nil {
		t.Fatal("a row without a child must start nothing and log nothing")
	}
}

func TestAColdChildStartsBeforeTheRequestIsSent(t *testing.T) {
	port := freePort(t)
	row := childRow("cold", port)
	_, kids, m := roomOf(t, row)
	seen := make(chan bool, 1)
	c := claiming{inner: answering{port, seen}, machine: m, kids: kids, key: machineroom.KeyOf(row.ID), priority: machineroom.PriorityTurn, row: row}
	ch, err := c.Stream(context.Background(), core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if !<-seen {
		t.Fatal("the request must be sent only once the child's port answers")
	}
	kids.stop(row.ID, 1)
}

func TestAColdChildSwapsTheResidentOneOut(t *testing.T) {
	oldPort, newPort := freePort(t), freePort(t)
	old, in := childRow("old", oldPort), childRow("new", newPort)
	_, kids, m := roomOf(t, old, in)
	if !kids.start(old.ID, old.Port, old.Command, nil, "") {
		t.Fatal("the resident child's port never answered")
	}
	seen := make(chan bool, 1)
	c := claiming{inner: answering{newPort, seen}, machine: m, kids: kids, key: machineroom.KeyOf(in.ID), priority: machineroom.PriorityTurn, row: in}
	ch, err := c.Stream(context.Background(), core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if !<-seen {
		t.Fatal("the incoming child must answer before the request is sent")
	}
	if machineroom.Answers(oldPort) {
		t.Fatal("the resident child must be stopped before the incoming one starts")
	}
	kids.stop(in.ID, 1)
}

func TestAMemberThatLeftRefusesInsteadOfWaiting(t *testing.T) {
	_, kids, m := roomOf(t)
	m.Close()
	c := claiming{inner: refusing{}, machine: m, kids: kids, key: machineroom.KeyOf("x"), priority: machineroom.PriorityTurn, row: models.Model{ID: "x"}}
	got := make(chan error, 1)
	go func() {
		_, err := c.Stream(context.Background(), core.Request{})
		m.Live()
		got <- err
	}()
	select {
	case err := <-got:
		if !errors.Is(err, errLeft) {
			t.Fatalf("want the left-the-room refusal, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a claim on a member that left must refuse, not wait")
	}
}

func TestAChildStillLoadingIsNotStartedTwice(t *testing.T) {
	home := t.TempDir()
	port := freePort(t)
	marker := filepath.Join(home, "pids")
	command := "echo $$ >> " + marker + "; exec sleep 300"
	first := newChildren(home, models.Table{}, nil, nil)
	second := newChildren(home, models.Table{}, nil, nil)
	t.Cleanup(func() { first.close(); second.close() })
	done := make(chan bool, 2)
	go func() { done <- first.start("slow", port, command, nil, "") }()
	await(t, "the pid file to name the loading child", 5*time.Second, func() bool { return second.held("slow") != 0 })
	go func() { done <- second.start("slow", port, command, nil, "") }()
	time.Sleep(time.Second)
	b, _ := os.ReadFile(marker)
	pids := strings.Fields(string(b))
	if len(pids) != 1 {
		t.Fatalf("one child must be loading, got %d", len(pids))
	}
	pid, _ := strconv.Atoi(pids[0])
	syscall.Kill(pid, syscall.SIGKILL)
	for i := 0; i < 2; i++ {
		select {
		case ok := <-done:
			if ok {
				t.Fatal("a child that died before its port answered did not start")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a start must return once the child is gone")
		}
	}
}

func TestAStalePidFileNamesNobody(t *testing.T) {
	home := t.TempDir()
	kids := newChildren(home, models.Table{}, nil, nil)
	t.Cleanup(kids.close)
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	self := os.Getpid()
	stale := strconv.Itoa(self) + " " + strconv.FormatInt(startOf(self)+1, 10)
	if err := os.WriteFile(filepath.Join(home, "logs", "gone.pid"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	if held := kids.held("gone"); held != 0 {
		t.Fatalf("a pid whose start time differs is another process: held %d", held)
	}
	kids.stop("gone", 0)
}

func TestAChildRowIsSentToItsPortWhateverTheBaseURL(t *testing.T) {
	hit := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit <- r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	row := childRow("local", srv.Listener.Addr().(*net.TCPAddr).Port)
	r := &root{row: row, activeID: row.ID, baseURL: "http://127.0.0.1:1/v1"}
	ch, err := r.buildProvider().Stream(context.Background(), core.Request{Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	for ev := range ch {
		if f, ok := ev.(core.Fault); ok {
			t.Fatalf("a child row's request goes to the row's port, not the base URL: %v", f.Err)
		}
	}
	select {
	case path := <-hit:
		if path != "/v1/chat/completions" {
			t.Fatalf("path = %q", path)
		}
	default:
		t.Fatal("the row's port was never asked")
	}
}

type mintSeen struct {
	saw chan bool
}

func (m mintSeen) Stream(ctx context.Context, _ core.Request) (<-chan evt.Signal, error) {
	_, ok := core.MintFrom(ctx)
	m.saw <- ok
	ch := make(chan evt.Signal, 1)
	ch <- core.Done{}
	close(ch)
	return ch, nil
}

func TestTheMintRidesOnlyARemoteRowOfAMesh(t *testing.T) {
	_, kids, m := roomOf(t)
	saw := make(chan bool, 1)
	remote := models.Model{ID: "far", Window: 8192, MaxTokens: 2048, Reserve: 1024, KeepRecent: 2048, Role: models.RoleInteractive, BaseURL: "http://100.118.31.66:7777/v1", Provider: "openai"}
	table, err := models.New(remote)
	if err != nil {
		t.Fatal(err)
	}
	row, _ := table.Get("far")
	key := machineroom.KeyOf("far")
	c := claiming{inner: mintSeen{saw}, machine: m, kids: kids, key: key, priority: machineroom.PriorityTurn, row: row, mesh: true}
	ch, err := c.Stream(context.Background(), core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if !<-saw {
		t.Fatal("a remote row of a mesh sends its request to the home machine's front, the mint must ride it")
	}
	await(t, "the claim to release", 5*time.Second, func() bool { return len(m.Holders(key)) == 0 })
	c = claiming{inner: mintSeen{saw}, machine: m, kids: kids, key: key, priority: machineroom.PriorityTurn, row: row, mesh: false}
	ch, err = c.Stream(context.Background(), core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if <-saw {
		t.Fatal("a machine without the mesh setting must not carry the mint to its provider")
	}
	await(t, "the claim to release", 5*time.Second, func() bool { return len(m.Holders(key)) == 0 })
	child := childRow("kid", freePort(t))
	c = claiming{inner: mintSeen{saw}, machine: m, key: machineroom.KeyOf("kid"), priority: machineroom.PriorityTurn, row: child, mesh: true}
	ch, err = c.Stream(context.Background(), core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if <-saw {
		t.Fatal("a child row's request goes to its own port, the front is not involved and carries no mint")
	}
	await(t, "the claim to release", 5*time.Second, func() bool { return len(m.Holders(machineroom.KeyOf("kid"))) == 0 })
}

type speaking struct{}

func (speaking) Stream(context.Context, core.Request) (<-chan evt.Signal, error) {
	ch := make(chan evt.Signal, 1)
	ch <- core.Done{Usage: core.Usage{Prompt: 1000, CacheRead: 990, Completion: 12, DecodePerSecond: 88.5, PromptPerSecond: 2100}}
	close(ch)
	return ch, nil
}

func TestAServedRequestIsRecordedBesideTheChildsLog(t *testing.T) {
	home, kids, m := roomOf(t)
	c := claiming{inner: speaking{}, machine: m, kids: kids, key: machineroom.KeyOf("remote"), priority: machineroom.PriorityFleet, row: models.Model{ID: "remote"}}
	ch, err := c.Stream(context.Background(), core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	got := machineroom.Recent(home, "remote", 4)
	if len(got) != 1 || got[0].Prompt != 1000 || got[0].Cached != 990 || got[0].DecodePerSecond != 88.5 || got[0].Priority != machineroom.PriorityFleet {
		t.Fatalf("the request's usage and speeds must be on the record: %+v", got)
	}
}
