package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/frontend/web"
	"github.com/mrsirg97-rgb/rig/v3/models"
	machineroom "github.com/mrsirg97-rgb/rig/v3/room"
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
        body = json.dumps({"id": "chat-1", "choices": [{"message": {"role": "assistant", "content": "hello"}}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
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
	remote, err := machineroom.New(t.TempDir(), func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remote.Close)
	addrs := gateAddrs(t, 2)
	remoteDoor, err := machineroom.NewDoor(remote, machineroom.MeshConfig(addrs[0], []string{addrs[1]}, gateTestPeriod, gateTestWindow), []int64{machineroom.KeyOf(cold.ID)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remoteDoor.Close)
	homeDoor, err := machineroom.NewDoor(m, machineroom.MeshConfig(addrs[1], []string{addrs[0]}, gateTestPeriod, gateTestWindow), nil, []int64{machineroom.KeyOf(resident.ID), machineroom.KeyOf(cold.ID)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(homeDoor.Close)
	srv, err := web.New(web.Options{
		Home: home, CWD: t.TempDir(), Models: table, Crontab: newFakeCrontab(),
		Natives: []string{"bash", "read"}, Root: home, Machine: m, Door: homeDoor, Version: Version,
		Ready: func(ctx context.Context, row models.Model) error { return kids.ready(ctx, m, row) },
	})
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
