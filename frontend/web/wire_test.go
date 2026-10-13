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
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/models"
	"github.com/mrsirg97-rgb/rig/v3/room"
)

func wireServer(t *testing.T) (*Server, string, *httptest.Server, room.Member) {
	return wireServerDoor(t, nil, nil)
}

func wireServerDoor(t *testing.T, machine room.Member, door room.Door) (*Server, string, *httptest.Server, room.Member) {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
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
		m, err := room.New(home, func(int64) int { return 1 }, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(m.Close)
		machine = m
	}
	srv, err := New(Options{Home: home, CWD: testCWD, Models: table, Crontab: &fakeCrontab{}, Natives: []string{"bash", "read"}, Root: home, Machine: machine, Door: door, Version: "3.0.0"})
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
	machine, err := room.New(seedHome(t), func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(machine.Close)
	remote, err := room.New(seedHome(t), func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remote.Close)
	ports := freePorts(t, 2)
	remoteDoor, err := room.NewDoor(remote, room.MeshConfig("gate-test", ports[0], []string{ports[1]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remoteDoor.Close)
	door, err := room.NewDoor(machine, room.MeshConfig("gate-test", ports[1], []string{ports[0]}), nil, []int64{key})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(door.Close)
	srv, _, _, _ := wireServerDoor(t, machine, door)
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
