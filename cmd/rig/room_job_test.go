package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
	rig "github.com/mrsirg97-rgb/rig/v3"
	"github.com/mrsirg97-rgb/rig/v3/models"
	machineroom "github.com/mrsirg97-rgb/rig/v3/room"
	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
)

const (
	roomInstanceEnv = "RIG_TEST_ROOM_INSTANCE"
	roomInstanceDir = "RIG_TEST_ROOM_HOME"
)

func TestAJobStatusCrossesTheMachineRoom(t *testing.T) {
	if os.Getenv(roomInstanceEnv) == "1" {
		roomInstanceRole()
		return
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := shortTemp(t)
	binDir := t.TempDir()
	workDir := t.TempDir()
	spool := filepath.Join(scratch, "spool")

	bin := filepath.Join(binDir, "rig")
	if out, err := exec.Command("go", "build", "-o", bin, filepath.Join(root, "cmd", "rig")).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	writeFakeCrontab(t, binDir, spool)
	sandboxOff(t, scratch)

	home := filepath.Join(cfgDir(t, scratch), "scheduler")
	fake := newFakeCrontab()
	st := scratchStores(t, home, "/ws/room")
	if _, err := sched.Create(context.Background(), st, fake, sched.CreateInput{
		Name: "room", Command: "true", Cron: "0 5 * * *",
		Cwd: workDir,
	}, "/ws/room", "sess-room", bin+" run-job", cfgDir(t, scratch), fixedNow); err != nil {
		t.Fatal(err)
	}
	key := "j1"
	if err := os.WriteFile(spool, []byte(fake.text_()), 0o644); err != nil {
		t.Fatal(err)
	}

	role := exec.Command(os.Args[0], "-test.run", "^TestAJobStatusCrossesTheMachineRoom$", "-test.timeout", "60s")
	role.Env = append(os.Environ(), roomInstanceEnv+"=1", roomInstanceDir+"="+cfgDir(t, scratch))
	roleOut, err := role.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := role.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		role.Process.Kill()
		role.Wait()
	}()
	lines := make(chan string, 64)
	go func() {
		scan := bufio.NewScanner(roleOut)
		for scan.Scan() {
			lines <- scan.Text()
		}
		close(lines)
	}()

	ready := false
	deadline := time.Now().Add(15 * time.Second)
	for !ready {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("the instance role exited before it was ready")
			}
			ready = line == "ready"
		case <-time.After(15 * time.Second):
			t.Fatal("the instance role never became ready")
		}
		if time.Now().After(deadline) {
			t.Fatal("the instance role never became ready")
		}
	}

	run := exec.Command(bin, "run-job", key)
	run.Dir = workDir
	run.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+scratch,
		"XDG_CONFIG_HOME="+scratch,
		"RIG_HOME="+cfgDir(t, scratch),
	)
	out, rerr := run.CombinedOutput()
	t.Logf("run-job: %v\n%s", rerr, out)

	var states []string
	for len(states) < 2 {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("the instance role exited with %d statuses", len(states))
			}
			if after, found := strings.CutPrefix(line, "STATUS "); found {
				var st machineroom.JobStatus
				if err := json.Unmarshal([]byte(after), &st); err != nil {
					t.Fatalf("the role's line is not a status: %q (%v)", line, err)
				}
				states = append(states, st.State)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the instance role received %v, want running then done", states)
		}
	}
	if states[0] != machineroom.JobRunning || states[1] != machineroom.JobDone {
		t.Fatalf("the statuses arrived %v, want running then done", states)
	}
}

func roomInstanceRole() {
	cfgDir := os.Getenv(roomInstanceDir)
	engine, fleetroom := newFleet()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go engine.Start(ctx)
	kids := newChildren(cfgDir, models.Table{}, engine, nil, fleetroom.Add(rig.MemberDecision))
	machine, err := machineroom.New(cfgDir, machineroom.JobStatusSince, kids.places, kids.effects, nil)
	if err != nil {
		os.Stderr.WriteString("rig: room: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer machine.Close()
	fleetroom.Add(rig.MemberFrontend).Subscribe(ctx, func(err error, messages ...broadcast.Message) {
		for _, m := range messages {
			st, ok := m.Signal().(machineroom.JobStatus)
			if !ok {
				continue
			}
			b, merr := json.Marshal(st)
			if merr != nil {
				continue
			}
			os.Stdout.WriteString("STATUS " + string(b) + "\n")
		}
	})
	os.Stdout.WriteString("ready\n")
	<-ctx.Done()
}
