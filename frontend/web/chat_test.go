package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/testenv"
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
	srv, err := New(home, WithCWD(testCWD), WithModels(modelsTable(t)), WithCrontab(&fakeCrontab{}), WithNatives([]string{"bash", "read"}...), WithRoot(home), WithCommands([]core.Command{echoCmd{}}...), WithStatus(func(context.Context) Status {
		return Status{Model: "m1", Effort: "high", Session: "sess-1", Up: 12, Rows: []string{"projects held: 2"}}
	}))
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
	live := frames(t, ts.URL, tok, "5", 3, 3*time.Second)
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

func TestTheChatUnescapesThePromptOnBothDoors(t *testing.T) {
	srv, tok := newChatServer(t)
	h := srv.Handler()
	doReq(t, h, "POST", "/api/chat", strings.NewReader(`{"text":"//home/ng/quiet"}`), originHdr(tok))
	if line, err := srv.Input(context.Background()); err != nil || line != "/home/ng/quiet" {
		t.Fatalf("the quiet prompt's escape = (%q, %v), want /home/ng/quiet", line, err)
	}
	doReq(t, h, "POST", "/api/chat", strings.NewReader(`{"text":"//home/ng/steered"}`), originHdr(tok))
	if line, err := srv.Input(context.Background()); err != nil || line != "/home/ng/steered" {
		t.Fatalf("the steered escape = (%q, %v), want /home/ng/steered", line, err)
	}
}

type steeredThrough struct {
	c *chat
}

func (s steeredThrough) Name() string { return "steer" }
func (s steeredThrough) Description() string {
	return "a line for the model at the next turn boundary, latest wins"
}
func (s steeredThrough) Run(ctx context.Context, args string, env any) (string, error) {
	s.c.Steer(args)
	return "", nil
}

func TestTheSteeredCommandDispatchesAtReEntry(t *testing.T) {
	srv, tok := newChatServer(t)
	h := srv.Handler()
	srv.chat.commands["steer"] = steeredThrough{c: srv.chat}
	srv.chat.known = append(srv.chat.known, "steer")
	doReq(t, h, "POST", "/api/chat", strings.NewReader(`{"text":"/steer /nope"}`), originHdr(tok))
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	done := make(chan string, 1)
	go func() {
		line, _ := srv.Input(ctx)
		done <- line
	}()
	<-ctx.Done()
	if line := <-done; line != "" {
		t.Fatalf("a dispatched command must never become a prompt, got %q", line)
	}
	_, replay, _ := srv.chat.subscribe(0)
	dispatched, prompted := false, false
	for _, raw := range replay {
		if strings.Contains(string(raw), `"kind":"command"`) && strings.Contains(string(raw), "unknown command: nope") {
			dispatched = true
		}
		if strings.Contains(string(raw), `"kind":"prompt"`) && strings.Contains(string(raw), "/nope") {
			prompted = true
		}
	}
	if !dispatched {
		t.Fatalf("the steered /-command dispatches at re-entry: %v", replay)
	}
	if prompted {
		t.Fatalf("the steered /-command reaches the model raw: %v", replay)
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

func TestSwarmAndDelegateStatusesSitSideBySide(t *testing.T) {
	srv, tok := newChatServer(t)
	h := srv.Handler()
	srv.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{{ID: 1, Role: "delegate", Task: "t1", State: "running"}}})
	srv.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{{ID: 2, Role: "worker", Task: "t3", State: "running"}}, Pending: 2})
	rec := doReq(t, h, "GET", "/api/swarm", nil, bearer(tok))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"task":"t3"`) || !strings.Contains(rec.Body.String(), `"pending":2`) {
		t.Fatalf("swarm: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"delegate":[{"`) || !strings.Contains(rec.Body.String(), `"task":"t1"`) {
		t.Fatalf("the delegate's rows are not their own: %s", rec.Body.String())
	}
	srv.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{{ID: 2, Role: "worker", State: "exited"}}})
	rec = doReq(t, h, "GET", "/api/swarm", nil, bearer(tok))
	if !strings.Contains(rec.Body.String(), `"delegate":[{"`) || !strings.Contains(rec.Body.String(), `"task":"t1"`) {
		t.Fatalf("the swarm's drained status took the delegate's rows: %s", rec.Body.String())
	}
}

func TestALiveSubscriberSeesEveryDelta(t *testing.T) {
	srv, _ := newChatServer(t)
	one, _, _ := srv.chat.subscribe(0)
	two, _, _ := srv.chat.subscribe(0)
	deltas := []string{"hel", "lo", "wor", "ld"}
	for _, d := range deltas {
		srv.Notify(core.TextDelta{Text: d})
	}
	for _, ch := range []chan []byte{one, two} {
		var got []string
		for range deltas {
			select {
			case raw := <-ch:
				var f map[string]any
				if err := json.Unmarshal(raw, &f); err != nil {
					t.Fatal(err)
				}
				text, ok := f["text"].(string)
				if f["kind"] != "text" || !ok {
					t.Fatalf("the subscriber must see the delta frames whole: %v", f)
				}
				got = append(got, text)
			case <-time.After(2 * time.Second):
				t.Fatalf("the subscriber stopped after %d of %d deltas: %q", len(got), len(deltas), got)
			}
		}
		if strings.Join(got, "") != "helloworld" {
			t.Fatalf("the subscriber must see every delta in order: %q", got)
		}
	}
}

func TestTheReplayAfterACoalesceSkipsOnlyWhatWasSeen(t *testing.T) {
	srv, _ := newChatServer(t)
	ch, _, _ := srv.chat.subscribe(0)
	for _, d := range []string{"hel", "lo", "wor", "ld"} {
		srv.Notify(core.TextDelta{Text: d})
	}
	var first int64
	select {
	case raw := <-ch:
		var f map[string]any
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		if f["kind"] != "text" {
			t.Fatalf("the run's first delta must cross as its own frame: %v", f)
		}
		first = int64(f["seq"].(float64))
	case <-time.After(2 * time.Second):
		t.Fatal("the first delta never crossed")
	}
	_, run, _ := srv.chat.subscribe(first - 1)
	if len(run) != 1 || !strings.Contains(string(run[0]), "helloworld") {
		t.Fatalf("a client one behind the run must receive it whole: %v", run)
	}
	_, again, _ := srv.chat.subscribe(first)
	if len(again) != 1 || !strings.Contains(string(again[0]), `"text":"loworld"`) {
		t.Fatalf("a client holding the first delta's seq receives only what coalesced after it: %v", again)
	}
	_, tail, _ := srv.chat.subscribe(first + 2)
	if len(tail) != 1 || !strings.Contains(string(tail[0]), `"text":"ld"`) {
		t.Fatalf("a client holding a mid-run seq receives only the deltas past it: %v", tail)
	}
	_, _, hi := srv.chat.subscribe(first)
	if _, seen, _ := srv.chat.subscribe(hi); len(seen) != 0 {
		t.Fatalf("a client that saw the whole run must not replay it: %v", seen)
	}
}

type parkingCmd struct {
	mu      sync.Mutex
	entered chan struct{}
	done    chan struct{}
	live    int
	peak    int
}

func (p *parkingCmd) Name() string        { return "park" }
func (p *parkingCmd) Description() string { return "parks Run until released" }
func (p *parkingCmd) Run(_ context.Context, _ string, _ any) (string, error) {
	p.mu.Lock()
	p.live++
	if p.live > p.peak {
		p.peak = p.live
	}
	p.mu.Unlock()
	select {
	case p.entered <- struct{}{}:
	default:
	}
	<-p.done
	p.mu.Lock()
	p.live--
	p.mu.Unlock()
	return "parked", nil
}

func TestTwoDispatchesNeverOverlap(t *testing.T) {
	srv, _ := newChatServer(t)
	park := &parkingCmd{entered: make(chan struct{}, 2), done: make(chan struct{})}
	srv.chat.commands["park"] = park
	srv.chat.known = append(srv.chat.known, "park")
	var wg sync.WaitGroup
	wg.Add(2)
	for _, args := range []string{"one", "two"} {
		go func(args string) {
			defer wg.Done()
			srv.chat.dispatch(context.Background(), "/park "+args)
		}(args)
	}
	<-park.entered
	select {
	case <-park.entered:
		close(park.done)
		wg.Wait()
		t.Fatalf("a second Run entered while the first was parked: peak %d", park.peak)
	case <-time.After(250 * time.Millisecond):
	}
	close(park.done)
	wg.Wait()
	if park.peak != 1 {
		t.Fatalf("dispatch must serialize its command runs: peak concurrent Run = %d", park.peak)
	}
}

func TestANoticePublishesOneFrameWithItsSource(t *testing.T) {
	srv, tok := newChatServer(t)
	ts := testenv.Server(t, srv.Handler())
	srv.Notify(evt.Notice{Source: "decision", Text: "queue full, dropping the bash proposal"})
	got := frames(t, ts.URL, tok, "0", 2, 3*time.Second)
	if k := kinds(got); k != "hello,notice" {
		t.Fatalf("kinds %s", k)
	}
	if got[1]["source"] != "decision" || got[1]["text"] != "queue full, dropping the bash proposal" {
		t.Fatalf("notice frame = %v", got[1])
	}
}

func TestTheReplayAfterAFinishedRunSkipsOnlyWhatWasSeen(t *testing.T) {
	srv, _ := newChatServer(t)
	ch, _, _ := srv.chat.subscribe(0)
	for _, d := range []string{"hel", "lo", "wor", "ld"} {
		srv.Notify(core.TextDelta{Text: d})
	}
	var first int64
	select {
	case raw := <-ch:
		var f map[string]any
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		first = int64(f["seq"].(float64))
	case <-time.After(2 * time.Second):
		t.Fatal("the first delta never crossed")
	}
	srv.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "bash"}})
	time.Sleep(100 * time.Millisecond)
	_, run, _ := srv.chat.subscribe(first)
	var texts []string
	for _, raw := range run {
		var f map[string]any
		if json.Unmarshal(raw, &f) == nil && f["kind"] == "text" {
			texts = append(texts, f["text"].(string))
		}
	}
	if len(texts) != 1 || texts[0] != "loworld" {
		t.Fatalf("a client holding the first delta must replay only the unseen suffix of the finished run, got %q", texts)
	}
}
