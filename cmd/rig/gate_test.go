package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/frontend/web"
	"github.com/mrsirg97-rgb/rig/v3/models"
	machineroom "github.com/mrsirg97-rgb/rig/v3/room"
	"github.com/mrsirg97-rgb/rig/v3/testenv"
)

const (
	gateTestPeriod = 50 * time.Millisecond
	gateTestWindow = 4
)

const fakeChildScript = `import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

class Reply(BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length", 0)))
        body = b'data: ' + json.dumps({"id": "chat-1", "choices": [{"delta": {"content": "hello"}, "finish_reason": "stop"}]}).encode() + b'\n\ndata: [DONE]\n\n'
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        self.send_response(200)
        self.end_headers()

    def log_message(self, *args):
        pass

HTTPServer(("127.0.0.1", int(sys.argv[1])), Reply).serve_forever()
`

func fakeChildRow(t *testing.T, id string) models.Model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-child.py")
	if err := os.WriteFile(path, []byte(fakeChildScript), 0o644); err != nil {
		t.Fatal(err)
	}
	return models.Model{
		ID: id, Window: 8192, MaxTokens: 2048, Reserve: 1024, KeepRecent: 2048, Role: models.RoleWorker,
		Command: "python3 " + path + " ${PORT}", Port: freePort(t), Places: 1, StopGrace: 1,
	}
}

func gateAddrs(t *testing.T, n int) []string {
	t.Helper()
	addrs := make([]string, n)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	}
	return addrs
}

func TestARemoteRequestStartsTheColdChildAndSwapsTheResidentOut(t *testing.T) {
	resident := fakeChildRow(t, "resident")
	cold := fakeChildRow(t, "cold")
	table, err := models.New(resident, cold)
	if err != nil {
		t.Fatal(err)
	}
	home, kids, m := roomOf(t, resident, cold)
	if !kids.start(resident.ID, resident.Port, resident.Command, nil, "") {
		t.Fatal("the resident child's port never answered")
	}
	remote, err := machineroom.New(shortTemp(t), Version, func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remote.Close)
	addrs := gateAddrs(t, 2)
	remoteDoor, err := machineroom.NewDoor(remote, machineroom.MeshConfig(addrs[0], []string{addrs[1]}, gateTestPeriod, gateTestWindow), []int64{machineroom.KeyOf(cold.ID)}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remoteDoor.Close)
	homeDoor, err := machineroom.NewDoor(m, machineroom.MeshConfig(addrs[1], []string{addrs[0]}, gateTestPeriod, gateTestWindow), nil, []int64{machineroom.KeyOf(resident.ID), machineroom.KeyOf(cold.ID)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(homeDoor.Close)
	srv, err := web.New(home,
		web.WithCWD(t.TempDir()), web.WithModels(table), web.WithCrontab(newFakeCrontab()),
		web.WithNatives("bash", "read"), web.WithRoot(home), web.WithMachine(m), web.WithDoor(homeDoor), web.WithVersion(Version),
		web.WithReady(func(ctx context.Context, row models.Model) error { return kids.ready(ctx, m, row) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	await(t, "the home door's clock to run", 30*time.Second, func() bool {
		return homeDoor.Horizon() > 0
	})
	claimed := make(chan broadcast.Mint, 1)
	remote.Claim(machineroom.KeyOf(cold.ID), machineroom.PriorityTurn, 0, func(mint broadcast.Mint, ok bool) { claimed <- mint })
	mint := <-claimed
	encoded, err := json.Marshal(mint)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"cold","messages":[]}`))
	req.Header.Set(core.MintHeader, string(encoded))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("a remote request the fold holds must be served by the cold child: got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "chat-1") {
		t.Fatalf("the cold child's reply must pass through: %q", rec.Body.String())
	}
	await(t, "the cold child to answer its port", 30*time.Second, func() bool {
		return machineroom.Answers(cold.Port)
	})
	await(t, "the resident child to be swapped out", 30*time.Second, func() bool {
		return !machineroom.Answers(resident.Port)
	})
	remote.Release(mint, machineroom.PriorityTurn)
}

func writeServeHome(t *testing.T, scratch string, rows, settings string) {
	t.Helper()
	dir := filepath.Join(scratch, ".rig")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte("["+rows+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
}

func startServe(t *testing.T, bin, scratch, addr, binDir string) {
	t.Helper()
	log := &lockedBuffer{}
	cmd := exec.Command(bin, "serve", "-addr", "127.0.0.1:"+addr)
	cmd.Env = append(scrubSwap(os.Environ()),
		"HOME="+scratch, "XDG_CONFIG_HOME="+scratch,
		"RIG_MODEL=fake-child",
		"RIG_SWAP_URL="+testenv.ClosedSwapURL,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	cmd.Dir = scratch
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("serve %s:\n%s", addr, log.String())
		}
	})
}

func TestARemoteTurnCrossesTwoRealServes(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "rig")
	if out, err := exec.Command("go", "build", "-o", bin, filepath.Join(root, "cmd", "rig")).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	ports := make([]string, 5)
	for i := range ports {
		ports[i] = strconv.Itoa(freePort(t))
	}
	front2, front1, door2, door1 := ports[0], ports[1], ports[2], ports[3]
	child := fakeChildRow(t, "fake-child")
	childRow := fmt.Sprintf(`{"id": "fake-child", "window": 8192, "maxTokens": 2048, "reserve": 1024, "keepRecent": 2048, "role": "worker", "command": %q, "port": %d, "places": 1, "stopGrace": 1}`, child.Command, child.Port)
	remoteRow := fmt.Sprintf(`{"id": "fake-child", "window": 8192, "maxTokens": 2048, "reserve": 1024, "keepRecent": 2048, "role": "worker", "provider": "openai", "baseUrl": "http://127.0.0.1:%s/v1"}`, front2)

	home2 := shortTemp(t)
	writeServeHome(t, home2, childRow, fmt.Sprintf(`{"sandbox":"off","swapUrl":%q,"mesh":{"listen":"127.0.0.1:%s","peers":["127.0.0.1:%s"]}}`, testenv.ClosedSwapURL, door2, door1))
	home1 := shortTemp(t)
	writeServeHome(t, home1, remoteRow, fmt.Sprintf(`{"sandbox":"off","swapUrl":%q,"mesh":{"listen":"127.0.0.1:%s","peers":["127.0.0.1:%s"]}}`, testenv.ClosedSwapURL, door1, door2))
	home3 := shortTemp(t)
	writeServeHome(t, home3, remoteRow, `{"sandbox":"off","swapUrl":"`+testenv.ClosedSwapURL+`"}`)

	startServe(t, bin, home2, front2, binDir)
	startServe(t, bin, home1, front1, binDir)
	for _, addr := range []string{front2, front1} {
		await(t, "the front to answer", 60*time.Second, func() bool {
			resp, err := http.Get("http://127.0.0.1:" + addr + "/v1/models")
			if err != nil {
				return false
			}
			resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		})
	}

	turn := func(scratch string) (string, time.Duration) {
		env := append(scrubSwap(os.Environ()),
			"HOME="+scratch, "XDG_CONFIG_HOME="+scratch,
			"RIG_SWAP_URL="+testenv.ClosedSwapURL,
			"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		)
		started := time.Now()
		cmd := exec.Command(bin, "-p", "hi", "-model", "fake-child")
		cmd.Env = env
		cmd.Dir = scratch
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the turn failed: %v\n%s", err, out)
		}
		return string(out), time.Since(started)
	}

	plain, plainTook := turn(home3)
	if !strings.Contains(plain, "hello") {
		t.Fatalf("the front's own floor claim must serve the row: %q", plain)
	}
	await(t, "the cold child to answer its port", 30*time.Second, func() bool {
		return machineroom.Answers(child.Port)
	})
	meshed, meshedTook := turn(home1)
	if !strings.Contains(meshed, "hello") {
		t.Fatalf("the remote peer's turn must come back through the gate: %q", meshed)
	}
	await(t, "the front to record the request it served", 30*time.Second, func() bool {
		return len(machineroom.Recent(filepath.Join(home2, ".rig"), "fake-child", 4)) == 1
	})
	recorded := machineroom.Recent(filepath.Join(home2, ".rig"), "fake-child", 4)[0]
	if recorded.Priority != machineroom.PriorityFleet {
		t.Fatalf("the record carries the remote claim's class: got %d", recorded.Priority)
	}
	t.Logf("the turn across the two serves took %s (the front's own floor claim, cold child included: %s); the served record carries the class %d at %s", meshedTook, plainTook, recorded.Priority, recorded.At.Format(time.RFC3339))
}
