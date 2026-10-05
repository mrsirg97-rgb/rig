package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

var wireDumpFlag = flag.String("wire-dump", "", "render the wire's request bodies into this directory")

var wireRootFlag = flag.String("rig-root", "../..", "the rig tree the dump builds and drives")

func TestWireDump(t *testing.T) {
	if *wireDumpFlag == "" {
		t.Skip("render: pass -wire-dump")
	}
	dumpWire(t, *wireDumpFlag, *wireRootFlag)
}

func TestWireDumpRendersByteIdenticalFromTwoWorlds(t *testing.T) {
	a := filepath.Join(t.TempDir(), "base")
	b := filepath.Join(t.TempDir(), "head")
	dumpWire(t, a, "../..")
	dumpWire(t, b, "../..")
	for _, name := range []string{"oneshot.json", "repl.json", "runjob.json", "tools.json", "menu.txt"} {
		ab, err := os.ReadFile(filepath.Join(a, name))
		if err != nil {
			t.Fatal(err)
		}
		bb, err := os.ReadFile(filepath.Join(b, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(ab, bb) {
			t.Fatalf("%s drifts between two worlds (the CI diff depends on the render being machine-independent):\na: %s\nb: %s", name, ab, bb)
		}
	}
}

func TestWireDumpArtifactsCarryTheWireTheyRender(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dump")
	dumpWire(t, dir, "../..")
	tools, err := os.ReadFile(filepath.Join(dir, "tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "oneshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, tools) {
		t.Fatal("tools.json must be the request's own tools array, verbatim")
	}
	menu, err := os.ReadFile(filepath.Join(dir, "menu.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var specs []struct {
		Function struct {
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(tools, &specs); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, s := range specs {
		total += len(s.Function.Description) + len(s.Function.Parameters)
	}
	if got, err := parseMenu(menu); err != nil || got != total {
		t.Fatalf("menu.txt = %v (err %v), want the tools' own arithmetic %d", string(menu), err, total)
	}
}

func parseMenu(b []byte) (int, error) {
	var n int
	_, err := fmt.Sscanf(string(b), "%d", &n)
	return n, err
}

func buildBinAt(t *testing.T, binDir, root string) string {
	t.Helper()
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, "rig")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/rig")
	cmd.Dir = abs
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func dumpWire(t *testing.T, out, root string) {
	t.Helper()
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := buildBinAt(t, t.TempDir(), root)

	s := &bodySrv{}
	srv := newBodySrv(t, s)
	scratch := t.TempDir()
	cmdDir := t.TempDir()
	cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
	cmd.Dir = cmdDir
	cmd.Env = rigEnv(scratch, "", "RIG_SWAP_URL="+srv.URL)
	if outp, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the oneshot run must succeed: %v\n%s", err, outp)
	}
	if s.count() != 1 {
		t.Fatalf("requests = %d, want 1", s.count())
	}
	oneshot := stripSession(t, s.last(), cmdDir, scratch)
	writeArtifact(t, out, "oneshot.json", oneshot)
	tools, menu := toolsAndMenu(t, oneshot)
	writeArtifact(t, out, "tools.json", tools)
	writeArtifact(t, out, "menu.txt", []byte(fmt.Sprintf("%d\n", menu)))

	s2 := &bodySrv{}
	srv2 := newBodySrv(t, s2)
	scratch2 := t.TempDir()
	cmdDir2 := t.TempDir()
	cmd = exec.Command(bin, "-base-url", srv2.URL+"/v1")
	cmd.Dir = cmdDir2
	cmd.Env = rigEnv(scratch2, "", "RIG_SWAP_URL="+srv2.URL)
	cmd.Stdin = strings.NewReader("hello\n")
	outp, _ := cmd.CombinedOutput()
	if len(outp) == 0 {
		t.Fatal("the repl run printed nothing")
	}
	if s2.count() != 1 {
		t.Fatalf("requests = %d, want 1", s2.count())
	}
	writeArtifact(t, out, "repl.json", stripSession(t, s2.last(), cmdDir2, scratch2))

	s3 := &bodySrv{model: "brain"}
	srv3 := newBodySrv(t, s3)
	scratch3 := t.TempDir()
	workDir := t.TempDir()
	shimDir := t.TempDir()
	writeFakeCrontab(t, shimDir, filepath.Join(scratch3, "spool"))
	home := filepath.Join(cfgDir(t, scratch3), "scheduler")
	fake := newFakeCrontab()
	st := scratchStores(t, home, "/ws/golden")
	if err := os.WriteFile(filepath.Join(cfgDir(t, scratch3), "models.json"),
		[]byte(`[{"id": "brain", "window": 65536, "maxTokens": 8192, "reserve": 8192, "keepRecent": 16384, "role": "worker"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Create(context.Background(), st, fake, sched.CreateInput{
		Name: "golden", Prompt: "say hi", Cron: "0 5 * * *",
		Cwd: workDir, Model: "brain", Busy: "skip",
	}, "/ws/golden", "sess-golden", bin+" run-job", cfgDir(t, scratch3), fixedNow); err != nil {
		t.Fatalf("create: %v", err)
	}
	key := "j1"
	if err := os.WriteFile(filepath.Join(scratch3, "spool"),
		[]byte("0 5 * * * "+bin+" run-job # rig-scheduler:"+sched.TagHome(cfgDir(t, scratch3))+":"+key+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sandboxOff(t, scratch3)

	cmd = exec.Command(bin, "run-job", key)
	cmd.Dir = workDir
	cmd.Env = append(rigEnv(scratch3, shimDir), "RIG_SWAP_URL="+srv3.URL)
	if outp, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("run-job exited non-zero (recorded outcomes exit 0): %v\n%s", runErr, outp)
	}
	if s3.count() != 1 {
		t.Fatalf("worker requests = %d, want 1 (the worker's chat call)", s3.count())
	}
	got := s3.last()
	escaped := strings.ReplaceAll(sched.ReportBack(workDir), "\n", `\n`)
	got = bytes.Replace(got, []byte(escaped), []byte(strings.ReplaceAll(sched.ReportBack("WORKDIR"), "\n", `\n`)), 1)
	writeArtifact(t, out, "runjob.json", stripSession(t, got, workDir, scratch3))
}

func stripSession(t *testing.T, data []byte, cwd, home string) []byte {
	t.Helper()
	section := sessionSection(cwd, home)
	if section == "" {
		return data
	}
	if !bytes.Contains(data, []byte(section)) {
		t.Fatalf("the request must carry the session section %q", section)
	}
	return bytes.Replace(data, []byte(`\n\n`+section), nil, 1)
}

func toolsAndMenu(t *testing.T, body []byte) (json.RawMessage, int) {
	t.Helper()
	var req struct {
		Tools json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal the captured body: %v", err)
	}
	if len(req.Tools) == 0 {
		t.Fatal("the request carries no tools")
	}
	var specs []struct {
		Function struct {
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(req.Tools, &specs); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, s := range specs {
		total += len(s.Function.Description) + len(s.Function.Parameters)
	}
	return req.Tools, total
}

func writeArtifact(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
