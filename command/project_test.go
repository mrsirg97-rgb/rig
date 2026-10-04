package command_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/command"
)

func TestProjectOpensASessionInTheNamedWorkspace(t *testing.T) {
	byName := allByName(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "workspace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	seen := ""
	swap := &swapSeam{dir: &seen, id: "s9"}
	env := &command.Env{NewSession: swap.swap}
	out, err := byName["project"].Run(context.Background(), "~"+string(filepath.Separator)+filepath.Base(dir), env)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if seen != dir {
		t.Fatalf("the seam must receive the expanded absolute directory, got %q want %q", seen, dir)
	}
	if !strings.Contains(out, "project: session s9") {
		t.Fatalf("the reply must carry the new session id:\n%s", out)
	}
	if !strings.Contains(out, "The session's workspace is "+dir) {
		t.Fatalf("the reply must carry the workspace line:\n%s", out)
	}
}

func TestProjectRefusesANonDirectoryByName(t *testing.T) {
	byName := allByName(t)
	swap := &swapSeam{id: "s9"}
	env := &command.Env{NewSession: swap.swap}
	out, err := byName["project"].Run(context.Background(), filepath.Join(t.TempDir(), "absent"), env)
	if err == nil || !strings.Contains(err.Error(), "project: not a directory:") {
		t.Fatalf("a non-directory must refuse by name, got (%q, %v)", out, err)
	}
	if swap.played {
		t.Fatal("the seam must not run for a refused project")
	}
}

func TestProjectRefusesBareArgs(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{NewSession: (&swapSeam{id: "s9"}).swap}
	if _, err := byName["project"].Run(context.Background(), "", env); err == nil || !strings.Contains(err.Error(), "project: usage: project <path>") {
		t.Fatalf("bare project must refuse with the usage, got %v", err)
	}
}

func TestProjectRefusesALiveTurn(t *testing.T) {
	byName := allByName(t)
	fs := &fakeSteer{live: true}
	env := &command.Env{Steer: fs, NewSession: (&swapSeam{id: "s9"}).swap}
	if _, err := byName["project"].Run(context.Background(), t.TempDir(), env); err == nil ||
		err.Error() != "project: a turn is live; steer or interrupt first" {
		t.Fatalf("a live turn must refuse, got %v", err)
	}
}

func TestNewKeepsTheWorkspace(t *testing.T) {
	byName := allByName(t)
	seen := "untouched"
	swap := &swapSeam{dir: &seen, id: "s2"}
	out, err := byName["new"].Run(context.Background(), "", &command.Env{NewSession: swap.swap})
	if err != nil || out != "new: session s2" {
		t.Fatalf("new = (%q, %v)", out, err)
	}
	if seen != "" {
		t.Fatalf("new must keep the current workspace, the seam saw %q", seen)
	}
}

type swapSeam struct {
	dir    *string
	id     string
	played bool
}

func (e *swapSeam) swap(ctx context.Context, dir string) (string, error) {
	e.played = true
	if e.dir != nil {
		*e.dir = dir
	}
	return e.id, nil
}
