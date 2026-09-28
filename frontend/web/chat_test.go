package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/config"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/testenv"
)

type echoCmd struct{}

func (echoCmd) Name() string        { return "echo" }
func (echoCmd) Description() string { return "echo the args" }
func (echoCmd) Run(_ context.Context, args string, _ any) (string, error) {
	if args == "fail" {
		return "", errors.New("echo: refused")
	}
	return "said " + args, nil
}

func newChatServer(t *testing.T) (*Server, string) {
	t.Helper()
	home := seedHome(t)
	srv, err := New(Options{
		Home: home, CWD: testCWD, Models: modelsTable(t), Workers: &config.Workers{Model: "worker-test", Slots: 1},
		Crontab: &fakeCrontab{}, Natives: []string{"bash", "read"}, Root: home,
		Commands: []core.Command{echoCmd{}},
		Status: func(context.Context) Status {
			return Status{Model: "m1", Effort: "high", Session: "sess-1", Up: 12, Rows: []string{"projects held: 2"}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	tok, _, err := srv.Token()
	if err != nil {
		t.Fatal(err)
	}
	srv.origins = []string{"http://127.0.0.1:7777"}
	return srv, tok
}

func originHdr(tok string) http.Header {
	h := bearer(tok)
	h.Set("Origin", "http://127.0.0.1:7777")
	h.Set("Content-Type", "application/json")
	return h
}

func frames(t *testing.T, base, tok string, since string, want int, within time.Duration) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/api/chat/events?since="+since, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := (&http.Client{Transport: testenv.Transport()}).Do(req)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	var out []map[string]any
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var f map[string]any
		if err := json.Unmarshal([]byte(line[6:]), &f); err != nil {
			t.Fatalf("frame %q: %v", line, err)
		}
		out = append(out, f)
		if len(out) >= want {
			return out
		}
	}
	return out
}

func kinds(fs []map[string]any) string {
	var ks []string
	for _, f := range fs {
		ks = append(ks, f["kind"].(string))
	}
	return strings.Join(ks, ",")
}

func TestChatPromptQueuesAndInputTakesIt(t *testing.T) {
	srv, tok := newChatServer(t)
	h := srv.Handler()
	rec := doReq(t, h, "POST", "/api/chat", strings.NewReader(`{"text":"hello there"}`), originHdr(tok))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"queued"`) {
		t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, "POST", "/api/chat", strings.NewReader(`{"text":"second"}`), originHdr(tok))
	if rec.Code != http.StatusConflict {
		t.Fatalf("a second prompt before the turn starts must be a 409, got %d", rec.Code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	line, err := srv.Input(ctx)
	if err != nil || line != "hello there" {
		t.Fatalf("Input = %q, %v", line, err)
	}
	if !srv.LiveTurn() {
		t.Fatal("a taken prompt marks the turn live")
	}
	_, replay, _ := srv.chat.subscribe(0)
	if len(replay) != 1 || !strings.Contains(string(replay[0]), `"kind":"prompt"`) {
		t.Fatalf("the taken prompt is published for every tab: %q", replay)
	}
}

func TestChatEventsReplayCoalesceAndLive(t *testing.T) {
	srv, tok := newChatServer(t)
	ts := testenv.Server(t, srv.Handler())
	srv.Notify(core.TextDelta{Text: "hel"})
	srv.Notify(core.TextDelta{Text: "lo"})
	srv.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"ls"}`)}})
	srv.Notify(core.ToolResult{ID: "c1", Content: strings.Repeat("x", resultCapBytes+10), Duration: 40 * time.Millisecond})
	srv.Notify(core.Done{Usage: core.Usage{Prompt: 10, Completion: 2}, Model: "m1"})
	srv.Notify(core.TurnEnd{Reason: core.TurnOver})
	got := frames(t, ts.URL, tok, "0", 6, 3*time.Second)
	if k := kinds(got); k != "hello,text,tool_start,tool_result,done,turn_end" {
		t.Fatalf("replay kinds %s", k)
	}
	if got[0]["replay"].(float64) != 5 || got[1]["text"] != "hello" {
		t.Fatalf("hello names the replay and deltas coalesce: %v %v", got[0], got[1])
	}
	if c := got[3]["content"].(string); !strings.HasSuffix(c, "· 10 more bytes hidden ·") {
		t.Fatalf("tool results are capped by name: %q", c[len(c)-40:])
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		srv.Notify(core.Fault{Err: errors.New("boom")})
	}()
	live := frames(t, ts.URL, tok, "4", 3, 3*time.Second)
	if k := kinds(live); k != "hello,turn_end,fault" {
		t.Fatalf("since=4 replays only what follows, then the live frame: %s", k)
	}
}

func TestChatCommandDispatchesNowAndPublishes(t *testing.T) {
	srv, tok := newChatServer(t)
	h := srv.Handler()
	rec := doReq(t, h, "POST", "/api/chat", strings.NewReader(`{"text":"/echo hi there"}`), originHdr(tok))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"text":"said hi there"`) {
		t.Fatalf("command reply: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, "POST", "/api/chat", strings.NewReader(`{"text":"/echo fail"}`), originHdr(tok))
	if !strings.Contains(rec.Body.String(), `"err":"echo: refused"`) {
		t.Fatalf("command error rides the reply: %s", rec.Body.String())
	}
	rec = doReq(t, h, "POST", "/api/chat", strings.NewReader(`{"text":"/nope"}`), originHdr(tok))
	if !strings.Contains(rec.Body.String(), "unknown command: nope (known: echo)") {
		t.Fatalf("unknown command names the known set: %s", rec.Body.String())
	}
	_, replay, _ := srv.chat.subscribe(0)
	if len(replay) != 3 || !strings.Contains(string(replay[0]), `"kind":"command"`) {
		t.Fatalf("every dispatch is published: %d", len(replay))
	}
	select {
	case line := <-srv.chat.lines:
		t.Fatalf("a command never reaches the loop as a prompt: %q", line)
	default:
	}
}

func TestChatSteerWhileLiveCancelsAndQueues(t *testing.T) {
	srv, tok := newChatServer(t)
	h := srv.Handler()
	turn, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := core.WithInterrupt(turn, cancel)
	doReq(t, h, "POST", "/api/chat", strings.NewReader(`{"text":"first"}`), originHdr(tok))
	if line, err := srv.Input(ctx); err != nil || line != "first" {
		t.Fatalf("Input = %q, %v", line, err)
	}
	rec := doReq(t, h, "POST", "/api/chat", strings.NewReader(`{"text":"actually, stop"}`), originHdr(tok))
	if !strings.Contains(rec.Body.String(), `"kind":"steer"`) {
		t.Fatalf("a prompt during a live turn steers: %s", rec.Body.String())
	}
	select {
	case <-turn.Done():
	case <-time.After(time.Second):
		t.Fatal("steer must cancel the live turn")
	}
	next, err := srv.Input(context.Background())
	if err != nil || next != "actually, stop" {
		t.Fatalf("the steered line is the next input: %q %v", next, err)
	}
}

func TestChatAskRoundTrip(t *testing.T) {
	srv, tok := newChatServer(t)
	h := srv.Handler()
	got := make(chan bool, 1)
	go func() { got <- srv.Ask(context.Background(), "run rm -rf build?") }()
	var id string
	deadline := time.Now().Add(2 * time.Second)
	for id == "" && time.Now().Before(deadline) {
		_, replay, _ := srv.chat.subscribe(0)
		for _, raw := range replay {
			var f map[string]any
			json.Unmarshal(raw, &f)
			if f["kind"] == "ask" {
				id = f["id"].(string)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("no ask frame published")
	}
	rec := doReq(t, h, "POST", "/api/chat/answer", strings.NewReader(`{"id":"`+id+`","yes":true}`), originHdr(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body.String())
	}
	select {
	case yes := <-got:
		if !yes {
			t.Fatal("the answer must reach Ask")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask did not return")
	}
	if rec = doReq(t, h, "POST", "/api/chat/answer", strings.NewReader(`{"id":"999","yes":true}`), originHdr(tok)); rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown question is a 404, got %d", rec.Code)
	}
}

func TestChatWalls(t *testing.T) {
	srv, tok := newChatServer(t)
	h := srv.Handler()
	if rec := doReq(t, h, "POST", "/api/chat", strings.NewReader(`{"text":"x"}`), bearer(tok)); rec.Code != http.StatusForbidden {
		t.Fatalf("no origin: %d", rec.Code)
	}
	if rec := doReq(t, h, "GET", "/api/chat", nil, bearer(tok)); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET chat: %d", rec.Code)
	}
	if rec := doReq(t, h, "POST", "/api/chat", strings.NewReader(`{"text":"  "}`), originHdr(tok)); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty text: %d", rec.Code)
	}
	if rec := doReq(t, h, "POST", "/api/chat/interrupt", nil, bearer(tok)); rec.Code != http.StatusForbidden {
		t.Fatalf("interrupt without origin: %d", rec.Code)
	}
	if rec := doReq(t, h, "GET", "/api/chat/events", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("events without the token: %d", rec.Code)
	}
}

func TestStatusAndSwarmRoutes(t *testing.T) {
	srv, tok := newChatServer(t)
	h := srv.Handler()
	rec := doReq(t, h, "GET", "/api/status", nil, bearer(tok))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"model":"m1"`) || !strings.Contains(rec.Body.String(), `"live":false`) {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	srv.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{{ID: 1, Role: "worker", Task: "t3", State: "running"}}, Pending: 2})
	rec = doReq(t, h, "GET", "/api/swarm", nil, bearer(tok))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"task":"t3"`) || !strings.Contains(rec.Body.String(), `"pending":2`) {
		t.Fatalf("swarm: %d %s", rec.Code, rec.Body.String())
	}
}
