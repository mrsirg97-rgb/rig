//go:build linux

package delegate_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v2/tool/delegate"
)

func TestTheIdleInterruptKillsTheWorkersProcessTree(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.sh")
	pidFile := filepath.Join(dir, "worker.pid")
	childFile := filepath.Join(dir, "child.pid")
	scriptBody := "#!/bin/sh\necho $$ > \"$RIG_TEST_WORKER_PID\"\nsleep 120 &\necho $! > \"$RIG_TEST_WORKER_CHILD\"\nwait\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RIG_TEST_WORKER_PID", pidFile)
	t.Setenv("RIG_TEST_WORKER_CHILD", childFile)

	spawned := make(chan struct{})
	tool := delegate.New(delegate.Opts{
		DB:           h.db,
		Home:         h.home,
		RigHome:      h.rigHome,
		StateDir:     filepath.Join(h.rigHome, "sessions"),
		SwapURL:      "http://127.0.0.1:8090",
		WorkerCmd:    []string{"/bin/sh", script},
		DefaultModel: "qwen3.8-workers",
		Sandbox:      "off",
		Fetch:        fakeFetch(""),
		Spawn: func(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
			close(spawned)
			return sched.RealSpawn(ctx, argv, cwd, env, observe)
		},
		Models: modelTable(t),
	})

	if _, err := tool.Exec(context.Background(), runArgs("hold the tree until the operator stops it")); err != nil {
		t.Fatalf("hand off: %v", err)
	}
	<-spawned
	waitUntil(t, "the worker and its child pids", func() bool {
		_, err1 := os.Stat(pidFile)
		_, err2 := os.Stat(childFile)
		return err1 == nil && err2 == nil
	})
	worker := readPID(t, pidFile)
	child := readPID(t, childFile)
	if err := syscall.Kill(child, 0); err != nil {
		t.Fatalf("the worker's child %d must be alive before the interrupt: %v", child, err)
	}

	tool.StopAll()

	waitUntil(t, "the death of the worker", func() bool { return processGone(worker) })
	waitUntil(t, "the death of the worker's child", func() bool { return processGone(child) })
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &pid); err != nil {
		t.Fatalf("pid file %s: %v", path, err)
	}
	if pid <= 0 {
		t.Fatalf("pid file %s holds %q", path, raw)
	}
	return pid
}

func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return errors.Is(err, syscall.ESRCH)
	}
	return processZombied(pid)
}

func processZombied(pid int) bool {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	body := string(raw)
	tail := strings.TrimSpace(body[strings.LastIndex(body, ")")+1:])
	return strings.HasPrefix(tail, "Z")
}
