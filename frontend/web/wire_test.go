package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/models"
	"github.com/mrsirg97-rgb/rig/v3/room"
)

const (
	meshTestPeriod = 50 * time.Millisecond
	meshTestWindow = 4
)

type stubDoor struct {
	holdings atomic.Int64
}

func (d *stubDoor) Id() int64          { return 1 }
func (d *stubDoor) Close()             {}
func (d *stubDoor) Machines() []string { return nil }
func (d *stubDoor) Horizon() int64     { return 0 }

func (d *stubDoor) Holding(int64, broadcast.Mint) (bool, string, int) {
	d.holdings.Add(1)
	return false, "", 0
}

func (d *stubDoor) Foreign(broadcast.Mint) bool               { return false }
func (d *stubDoor) Refused(int64, broadcast.Mint, int64) bool { return false }
func (d *stubDoor) Release(broadcast.Mint)                    {}

func wireServer(t *testing.T) (*Server, string, *httptest.Server, room.Member) {
	return wireServerDoor(t, nil, nil)
}

var upstreamMint chan string

func wireServerDoor(t *testing.T, machine room.Member, door room.Door) (*Server, string, *httptest.Server, room.Member) {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if upstreamMint != nil {
			upstreamMint <- r.Header.Get(core.MintHeader)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chat-1","choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	t.Cleanup(up.Close)
	port := up.Listener.Addr().(*net.TCPAddr).Port
	table, err := models.New(
		models.Model{ID: "fake-child", Window: 8192, MaxTokens: 2048, Reserve: 1024, KeepRecent: 2048, Role: models.RoleWorker, Command: "python3 -m http.server ${PORT}", Port: port, Places: 1, Aliases: []string{"fake-alias"}},
		models.Model{ID: "gpt-remote", Window: 128000, MaxTokens: 8192, Reserve: 16000, KeepRecent: 32000, Role: models.RoleInteractive, BaseURL: up.URL + "/v1", APIKey: "sk-test"},
	)
	if err != nil {
		t.Fatal(err)
	}
	home := seedHome(t)
	if machine == nil {
		m, err := room.New(home, room.JobStatusSince, func(int64) int { return 1 }, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(m.Close)
		machine = m
	}
	srv, err := New(home, WithCWD(testCWD), WithModels(table), WithCrontab(&fakeCrontab{}), WithNatives([]string{"bash", "read"}...), WithRoot(home), WithMachine(machine), WithDoor(door), WithVersion("3.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv, filepath.Join(home, "room"), up, machine
}

func TestTheDashboardPagesCarryTheRoomAndTheVersion(t *testing.T) {
	srv, _, _, _ := wireServer(t)
	tok, _, err := srv.Token()
	if err != nil {
		t.Fatal(err)
	}
	rec := doReq(t, srv.Handler(), "GET", "/api/room", nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("the room page: got %d", rec.Code)
	}
	var roomBody struct {
		Members []int64                   `json:"members"`
		Models  map[string]map[string]any `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &roomBody); err != nil {
		t.Fatal(err)
	}
	row, ok := roomBody.Models["fake-child"]
	if !ok || row["live"] != true {
		t.Fatalf("the room page must show what is resident: %s", rec.Body.String())
	}
	rec = doReq(t, srv.Handler(), "GET", "/api/update", nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("the update page: got %d", rec.Code)
	}
	var version struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}
	if version.Version != "3.0.0" {
		t.Fatalf("the update page must show the build's version: %s", rec.Body.String())
	}
}

func TestTheWireFrontServesTheRowsAndTheirAliases(t *testing.T) {
	srv, _, _, _ := wireServer(t)
	rec := doReq(t, srv.Handler(), "GET", "/v1/models", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the wire front answers without the dashboard's token: got %d", rec.Code)
	}
	var body struct {
		Data []struct {
			ID   string `json:"id"`
			Meta struct {
				LLamaSwap struct {
					Aliases []string `json:"aliases"`
				} `json:"llamaswap"`
			} `json:"meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	aliased := map[string][]string{}
	for _, row := range body.Data {
		seen[row.ID] = true
		aliased[row.ID] = row.Meta.LLamaSwap.Aliases
	}
	if !seen["fake-child"] || !seen["fake-alias"] || !seen["gpt-remote"] {
		t.Fatalf("the rows and their aliases must be listed: %v", seen)
	}
	if len(aliased["fake-child"]) != 1 || aliased["fake-child"][0] != "fake-alias" {
		t.Fatalf("the canonical row must carry its aliases beside it: %v", aliased["fake-child"])
	}
}

func TestTheWireFrontClaimsAndStreams(t *testing.T) {
	srv, _, _, machine := wireServer(t)
	rec := doReq(t, srv.Handler(), "POST", "/v1/chat/completions", jsonBody(t, map[string]any{"model": "fake-alias", "messages": []any{}}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the wire front must stream the child's reply: got %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != "chat-1" {
		t.Fatalf("the child's reply must pass through: %v", body)
	}
	await(t, "the claim to release at the stream's end", 5*time.Second, func() bool {
		return len(machine.Holders(room.KeyOf("fake-child"))) == 0
	})
}

func TestTheWireFrontUnloadsThroughTheRoom(t *testing.T) {
	srv, _, _, machine := wireServer(t)
	rec := doReq(t, srv.Handler(), "POST", "/unload", jsonBody(t, map[string]any{"model": "fake-alias"}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("unload: got %d %s", rec.Code, rec.Body.String())
	}
	await(t, "the stop to be a pinned act", 5*time.Second, func() bool {
		return len(machine.Waiting(room.KeyOf("fake-child"))) == 0
	})
}

func jsonBody(t *testing.T, v any) io.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

func await(t *testing.T, what string, within time.Duration, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s never happened within %s", what, within)
}

func TestTheWireFrontTakesALongContextBody(t *testing.T) {
	srv, _, _, _ := wireServer(t)
	big := `{"model":"fake-alias","messages":[{"role":"user","content":"` + strings.Repeat("a", 2<<20) + `"}]}`
	rec := doReq(t, srv.Handler(), "POST", "/v1/chat/completions", strings.NewReader(big), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("a 2 MiB request must pass through whole: got %d %s", rec.Code, rec.Body.String())
	}
}

func TestTheWireFrontOutlivesTheServersWriteTimeout(t *testing.T) {
	srv, _, _, _ := wireServer(t)
	srv.ready = func(context.Context, models.Model) error {
		time.Sleep(300 * time.Millisecond)
		return nil
	}
	front := httptest.NewUnstartedServer(srv.Handler())
	front.Config.WriteTimeout = 100 * time.Millisecond
	front.Start()
	t.Cleanup(front.Close)
	resp, err := http.Post(front.URL+"/v1/chat/completions", "application/json", jsonBody(t, map[string]any{"model": "fake-alias", "messages": []any{}}))
	if err != nil {
		t.Fatalf("a reply slower than the server's write timeout must still arrive: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "chat-1") {
		t.Fatalf("got %d %s", resp.StatusCode, body)
	}
}

func TestTheLogsTailReadsTheEndOfALongFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child.log")
	var b strings.Builder
	for i := 0; i < 20000; i++ {
		b.WriteString("line ")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := tail(path, 5)
	if len(got) != 5 || got[4] != "line 19999" || got[0] != "line 19995" {
		t.Fatalf("want the last five lines in order, got %v", got)
	}
	if all := tail(path, 1<<20); len(all) >= 20000 || all[0] == "line 0" || strings.Contains(all[0], "\n") {
		t.Fatalf("a long file is read from its end and never from a cut line: %d lines, first %q", len(all), all[0])
	}
	if none := tail(filepath.Join(t.TempDir(), "absent.log"), 5); len(none) != 0 {
		t.Fatalf("a model with no log reads empty, got %v", none)
	}
}

func TestTheFrontSGateServesAMintWhenTheFoldShowsIt(t *testing.T) {
	key := room.KeyOf("fake-child")
	machine, err := room.New(seedHome(t), room.JobStatusSince, func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(machine.Close)
	remote, err := room.New(seedHome(t), room.JobStatusSince, func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remote.Close)
	ports := freePorts(t, 2)
	remoteDoor, err := room.NewDoor(remote, room.MeshConfig(ports[0], []string{ports[1]}, meshTestPeriod, meshTestWindow), []int64{key}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remoteDoor.Close)
	door, err := room.NewDoor(machine, room.MeshConfig(ports[1], []string{ports[0]}, meshTestPeriod, meshTestWindow), nil, []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(door.Close)
	srv, _, _, _ := wireServerDoor(t, machine, door)
	upstreamMint = make(chan string, 1)
	t.Cleanup(func() { upstreamMint = nil })
	claimed := make(chan broadcast.Mint, 1)
	remote.Claim(key, room.PriorityTurn, 0, func(m broadcast.Mint, ok bool) { claimed <- m })
	mint := <-claimed
	await(t, "the mirror to hold the key in the home machine's fold", 20*time.Second, func() bool {
		for _, op := range machine.Holders(key) {
			if op.Priority() == room.PriorityTurn {
				return true
			}
		}
		return false
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	encoded, err := json.Marshal(mint)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", jsonBody(t, map[string]any{"model": "fake-child", "messages": []any{}}))
	req.Header.Set(core.MintHeader, string(encoded))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("a mint the fold shows must be served: got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "chat-1") {
		t.Fatalf("the gate must serve the reply through the mirror: got %q", rec.Body.String())
	}
	if got := <-upstreamMint; got != "" {
		t.Fatalf("the front must not forward the mint to the child it serves: %q", got)
	}
	await(t, "the front to record the request it served", 5*time.Second, func() bool {
		return len(room.Recent(srv.home, "fake-child", 4)) == 1
	})
	recorded := room.Recent(srv.home, "fake-child", 4)[0]
	if recorded.Priority != room.PriorityTurn {
		t.Fatalf("the record carries the remote claim's class: got %d", recorded.Priority)
	}
	if recorded.At.IsZero() {
		t.Fatal("the record carries the time the reply ended")
	}
	await(t, "the gate to have served without claiming (the fold holds no outside-floor claim)", 5*time.Second, func() bool {
		for _, op := range machine.Holders(key) {
			if op.Priority() == room.PriorityOutside {
				return false
			}
		}
		return true
	})
	remote.Release(mint, room.PriorityTurn)
	await(t, "the front's release to end the mirror at the reply's end", 20*time.Second, func() bool {
		return len(machine.Holders(key)) == 0
	})
}

func TestTheFrontSEndsAClaimNoDoorCarriesAsAnError(t *testing.T) {
	key := room.KeyOf("fake-alias")
	machine, err := room.New(seedHome(t), room.JobStatusSince, func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(machine.Close)
	remote, err := room.New(seedHome(t), room.JobStatusSince, func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remote.Close)
	ports := freePorts(t, 2)
	remoteDoor, err := room.NewDoor(remote, room.MeshConfig(ports[0], []string{ports[1]}, meshTestPeriod, meshTestWindow), []int64{key}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remoteDoor.Close)
	door, err := room.NewDoor(machine, room.MeshConfig(ports[1], []string{ports[0]}, meshTestPeriod, meshTestWindow), nil, []int64{room.KeyOf("fake-child")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(door.Close)
	srv, _, _, _ := wireServerDoor(t, machine, door)
	await(t, "the home door's clock to run", 20*time.Second, func() bool {
		return door.Horizon() > 0
	})
	claimed := make(chan broadcast.Mint, 1)
	remote.Claim(key, room.PriorityTurn, 0, func(m broadcast.Mint, ok bool) { claimed <- m })
	mint := <-claimed
	await(t, "the home door to see the claim under the key it does not carry", 20*time.Second, func() bool {
		return door.Foreign(mint)
	})
	encoded, err := json.Marshal(mint)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", jsonBody(t, map[string]any{"model": "fake-alias", "messages": []any{}}))
	req.Header.Set(core.MintHeader, string(encoded))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a claim no door carries must end as an error the caller reads: got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "fake-alias") {
		t.Fatalf("the refusal must name the model id: %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "clock") {
		t.Fatalf("the refusal must not blame the clock: %q", rec.Body.String())
	}
	t.Logf("debug: foreign=%v horizon=%d", door.Foreign(mint), door.Horizon())
	remote.Release(mint, room.PriorityTurn)
}

func TestTheFrontSEndsAClockRefusedClaimAsAnError(t *testing.T) {
	key := room.KeyOf("fake-child")
	machine, err := room.New(seedHome(t), room.JobStatusSince, func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(machine.Close)
	remote, err := room.New(seedHome(t), room.JobStatusSince, func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remote.Close)
	ports := freePorts(t, 2)
	remoteDoor, err := room.NewDoor(remote, room.MeshConfig(ports[0], []string{ports[1]}, meshTestPeriod, meshTestWindow), []int64{key}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remoteDoor.Close)
	door, err := room.NewDoor(machine, room.MeshConfig(ports[1], []string{ports[0]}, meshTestPeriod/2, meshTestWindow), nil, []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(door.Close)
	srv, _, _, _ := wireServerDoor(t, machine, door)
	claimed := make(chan broadcast.Mint, 1)
	remote.Claim(key, room.PriorityTurn, 0, func(m broadcast.Mint, ok bool) { claimed <- m })
	mint := <-claimed
	await(t, "the home door's clock to run", 20*time.Second, func() bool {
		return door.Horizon() > 0
	})
	encoded, err := json.Marshal(mint)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", jsonBody(t, map[string]any{"model": "fake-child", "messages": []any{}}))
	req.Header.Set(core.MintHeader, string(encoded))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a claim the home machine's clock refuses must end as an error the caller reads: got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "the machine's clock may be wrong") {
		t.Fatalf("the refusal must name the clock: got %q", rec.Body.String())
	}
	remote.Release(mint, room.PriorityTurn)
	await(t, "the remote release to leave the remote machine's room", 20*time.Second, func() bool {
		return len(remote.Holders(key)) == 0
	})
}

func TestAMintAtAFrontWithNoDoorIsServed(t *testing.T) {
	srv, _, _, _ := wireServer(t)
	encoded, _ := json.Marshal(broadcast.Mint{Step: 5, Member: 9, Nonce: 1})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", jsonBody(t, map[string]any{"model": "fake-child", "messages": []any{}}))
	req.Header.Set(core.MintHeader, string(encoded))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	t.Logf("mesh off, request carrying a mint: %d %s", rec.Code, rec.Body.String())
	if rec.Code != http.StatusOK {
		t.Fatal("3.0.4 served this request; with the mesh off it is refused")
	}
}

func TestTheGateMakesTheChildReadyForARemoteRequest(t *testing.T) {
	key := room.KeyOf("fake-child")
	machine, err := room.New(seedHome(t), room.JobStatusSince, func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(machine.Close)
	remote, err := room.New(seedHome(t), room.JobStatusSince, func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remote.Close)
	ports := freePorts(t, 2)
	remoteDoor, err := room.NewDoor(remote, room.MeshConfig(ports[0], []string{ports[1]}, meshTestPeriod, meshTestWindow), []int64{key}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remoteDoor.Close)
	door, err := room.NewDoor(machine, room.MeshConfig(ports[1], []string{ports[0]}, meshTestPeriod, meshTestWindow), nil, []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(door.Close)
	srv, _, _, _ := wireServerDoor(t, machine, door)
	var readied atomic.Int32
	srv.ready = func(context.Context, models.Model) error { readied.Add(1); return nil }
	claimed := make(chan broadcast.Mint, 1)
	remote.Claim(key, room.PriorityTurn, 0, func(m broadcast.Mint, ok bool) { claimed <- m })
	mint := <-claimed
	await(t, "the mirror to hold the key in the home machine's fold", 20*time.Second, func() bool {
		for _, op := range machine.Holders(key) {
			if op.Priority() == room.PriorityTurn {
				return true
			}
		}
		return false
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	encoded, _ := json.Marshal(mint)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", jsonBody(t, map[string]any{"model": "fake-child", "messages": []any{}}))
	req.Header.Set(core.MintHeader, string(encoded))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	t.Logf("remote request: %d, ready calls: %d", rec.Code, readied.Load())
	if readied.Load() == 0 {
		t.Fatal("the gate served a remote request without the ready path: a cold child is never started")
	}
}

func TestACanceledGateStopsPolling(t *testing.T) {
	door := &stubDoor{}
	srv, _, _, _ := wireServerDoor(t, nil, door)
	encoded, err := json.Marshal(broadcast.Mint{Step: 5, Member: 9, Nonce: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", jsonBody(t, map[string]any{"model": "fake-child", "messages": []any{}}))
	req.Header.Set(core.MintHeader, string(encoded))
	req = req.WithContext(ctx)
	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	cancel()
	<-done
	time.Sleep(4 * gatePoll)
	before := door.holdings.Load()
	time.Sleep(6 * gatePoll)
	if after := door.holdings.Load(); after != before {
		t.Fatalf("a canceled request must stop the door poll: %d checks became %d", before, after)
	}
}

func freePorts(t *testing.T, n int) []string {
	t.Helper()
	addrs := make([]string, n)
	for i := range addrs {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addrs[i] = ln.Addr().String()
		ln.Close()
	}
	return addrs
}
