package main

// The sandbox's door cases (SPEC_SANDBOX 1, 5, testing): the
// interactive REPL never consults the jail (the fake PATH shim stays
// untouched), and the run-job door is fail-closed — bwrap absent
// refuses loud (the outcome row carries it), sandbox off runs unjailed
// with the one loud line.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

// sandboxOffOptIn writes the scratch home's settings.json with the
// operator's explicit sandbox off (the door tests' choice: they pin
// the worker's environment, not the jail — the jail has its own door
// and fixture tests, SPEC_SANDBOX).
func sandboxOff(t *testing.T, scratch string) {
	t.Helper()
	dir := filepath.Join(scratch, ".rig")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"sandbox":"off"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bwrapShim drops a fake bwrap on PATH that records its invocation
// (the "the shim's marker stays untouched" witness, SPEC_SANDBOX).
func bwrapShim(t *testing.T, dir string) string {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "bwrap-fired")
	script := "#!/bin/sh\ntouch " + marker + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "bwrap"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return marker
}

// TestInteractiveREPLNeverConsultsTheJail (SPEC_SANDBOX, named): the
// REPL's golden path runs with zero sandbox code on it — the fake
// bwrap on PATH stays uninvoked, the run is byte-for-byte the model's
// reply.
func TestInteractiveREPLNeverConsultsTheJail(t *testing.T) {
	scratch := t.TempDir()
	binDir := t.TempDir()
	bin := buildBin(t, binDir)
	marker := bwrapShim(t, binDir)

	s := &pluginSrv{replies: []string{pongReply}}
	srv := newPluginSrv(t, s)

	cmd := exec.Command(bin, "-p", "hi")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(scratch, binDir)
	cmd.Env = append(cmd.Env, "RIG_BASE_URL="+srv.URL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the REPL golden path must run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "pong") {
		t.Fatalf("the reply must be the model's, got %q", out)
	}
	if s.count() != 1 {
		t.Fatalf("exactly one model call, got %d", s.count())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the interactive REPL must never invoke bwrap (the shim fired: stat %v)", err)
	}
}

// TestRunJobDoorFailClosedWithoutBwrap (SPEC_SANDBOX 1, named): the
// door's fail-closed refusal — bwrap absent (PATH scrubbed) refuses
// the run; the outcome row carries it (bwrap and the profile named,
// both settings keys taught); the worker never spawns.
func TestRunJobDoorFailClosedWithoutBwrap(t *testing.T) {
	scratch := t.TempDir()
	// the scrubbed PATH: the door's directory only (rig, crontab — no
	// bwrap, no /usr/bin).
	binDir := t.TempDir()
	bin := buildBin(t, binDir)
	writeFakeCrontab(t, binDir, filepath.Join(scratch, "spool"))
	spooDir := filepath.Join(scratch, "spool")

	home := filepath.Join(scratch, ".rig", "scheduler")
	fake := newFakeCrontab()
	st := scratchStores(t, home, "/ws/door2")
	if _, err := sched.Create(context.Background(), st, fake, sched.CreateInput{
		Name: "door2", Prompt: "say hi", Cron: "0 5 * * *", Scope: "cwd",
		Cwd: t.TempDir(), Model: "qwen3.8-workers", Busy: "skip",
	}, "/ws/door2", "sess-door2", bin+" run-job", fixedNow); err != nil {
		t.Fatalf("create: %v", err)
	}
	key := "cwd-" + sched.CwdHash("/ws/door2") + ":j1"
	if err := os.WriteFile(spooDir, []byte(fake.text_()), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "run-job", key)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PATH="+binDir, "HOME="+scratch, "XDG_CONFIG_HOME="+scratch)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("recorded outcomes exit 0: %v\n%s", runErr, out)
	}
	if rows := countRows(t, st, `SELECT count(*) FROM runs`); rows != 1 {
		t.Fatalf("runs rows = %d, want 1 (the refusal is a record, not a fault)", rows)
	}
	var status, reason string
	if err := st.Cwd.DB.QueryRow(`SELECT last_status FROM jobs WHERE id = 'j1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := st.Cwd.DB.QueryRow(`SELECT reason FROM runs ORDER BY seq DESC LIMIT 1`).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if status != "skip" {
		t.Fatalf("the refusal is a skip, got %q", status)
	}
	for _, needle := range []string{"bwrap", "jailed", "sandbox", "sandboxBinds"} {
		if !strings.Contains(reason, needle) {
			t.Fatalf("the refusal must name %q, got %q", needle, reason)
		}
	}
}

// TestRunJobDoorSandboxOffRunsUnjailed (SPEC_SANDBOX 5, named): the
// operator's explicit act at the door — one loud line per worker run,
// the worker spawns as today (the bwrap shim stays uninvoked).
func TestRunJobDoorSandboxOffRunsUnjailed(t *testing.T) {
	scratch := t.TempDir()
	binDir := t.TempDir()
	bin := buildBin(t, binDir)
	marker := bwrapShim(t, binDir)
	writeFakeCrontab(t, binDir, filepath.Join(scratch, "spool"))
	spooDir := filepath.Join(scratch, "spool")

	home := filepath.Join(scratch, ".rig", "scheduler")
	if err := os.MkdirAll(filepath.Join(scratch, ".rig"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratch, ".rig", "settings.json"), []byte(`{"sandbox":"off"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := newFakeCrontab()
	st := scratchStores(t, home, "/ws/door3")
	workDir := t.TempDir()
	if _, err := sched.Create(context.Background(), st, fake, sched.CreateInput{
		Name: "door3", Prompt: "say hi", Cron: "0 5 * * *", Scope: "cwd",
		Cwd: workDir, Model: "qwen3.8-workers", Busy: "skip",
	}, "/ws/door3", "sess-door3", bin+" run-job", fixedNow); err != nil {
		t.Fatalf("create: %v", err)
	}
	key := "cwd-" + sched.CwdHash("/ws/door3") + ":j1"
	if err := os.WriteFile(spooDir, []byte(fake.text_()), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "run-job", key)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+scratch,
		"XDG_CONFIG_HOME="+scratch,
		"RIG_SWAP_URL="+newSwapServer(t).URL, // the worker's POST 404s: the run faults (the off line is the point)
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("run-job exited non-zero (recorded outcomes exit 0): %v\n%s", runErr, out)
	}
	if got := strings.Count(string(out), "sandbox off"); got != 1 {
		t.Fatalf("exactly one loud line per worker run (got %d), output %q", got, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("sandbox off must never invoke bwrap (the shim fired: stat %v)", err)
	}
	var status string
	if err := st.Cwd.DB.QueryRow(`SELECT last_status FROM jobs WHERE id = 'j1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "fail" {
		t.Fatalf("the off run is a run (the dead endpoint faults it), got %q", status)
	}
}
