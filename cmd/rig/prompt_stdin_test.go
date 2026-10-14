package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/tool/file"
)

func TestPromptDashReadsStdinWhole(t *testing.T) {
	s := &bodySrv{}
	srv := newBodySrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := shortTemp(t)
	prompt := strings.Repeat("review the pending rows\n", 6000)
	cmd := exec.Command(bin, "-p", "-", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(t, scratch, "", "RIG_SWAP_URL="+srv.URL)
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("a 144 KB prompt on stdin must run (argv could never carry it): %v\n%s", err, out)
	}
	if s.count() != 1 {
		t.Fatalf("requests = %d, want 1", s.count())
	}
	if !strings.Contains(string(s.last()), strings.Repeat("review the pending rows\\n", 3)) || !strings.Contains(string(s.last()), "\"content\":\"review the pending rows") {
		t.Fatalf("the request must carry the stdin prompt as the user message")
	}
	if len(s.last()) < len(prompt) {
		t.Fatalf("the request body (%d bytes) is shorter than the prompt (%d): the prompt was cut", len(s.last()), len(prompt))
	}
}

func TestPromptDashWithNothingOnStdinRefuses(t *testing.T) {
	bin := buildBin(t, t.TempDir())
	cmd := exec.Command(bin, "-p", "-")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(t, shortTemp(t), "")
	cmd.Stdin = strings.NewReader("  \n")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("an empty stdin prompt must refuse, got:\n%s", out)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("the refusal exits 2 like the other flag refusals, got %v", err)
	}
	if !strings.Contains(string(out), "rig: -p -: nothing on stdin") {
		t.Fatalf("the refusal must name the carrier, got:\n%s", out)
	}
}

func TestPromptDashOverTheCeilingRefuses(t *testing.T) {
	bin := buildBin(t, t.TempDir())
	cmd := exec.Command(bin, "-p", "-", "-base-url", "http://127.0.0.1:1/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(t, shortTemp(t), "")
	cmd.Stdin = strings.NewReader(strings.Repeat("x", file.ReadCap+1))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a prompt over the read ceiling must refuse, got:\n%s", out)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("the ceiling refusal exits 2 like the other flag refusals, got %v", err)
	}
	if !strings.Contains(string(out), "rig: -p -:") || !strings.Contains(string(out), "ceiling") {
		t.Fatalf("the refusal must name the carrier and the ceiling, got:\n%s", out)
	}
}
