package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/middleware/paths"
)

type projectCmd struct{}

func (projectCmd) Name() string { return "project" }

func (projectCmd) Description() string {
	return "close this session and open a fresh one in another workspace"
}

func (projectCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(args)
	if dir == "" {
		return "", errors.New("project: usage: project <path>")
	}
	if liveTurn(e) {
		return "", errors.New("project: a turn is live; steer or interrupt first")
	}
	if e.NewSession == nil {
		return "", errors.New("project: no new-session seam (the root did not wire one)")
	}
	abs, err := filepath.Abs(paths.Expand(dir))
	if err != nil {
		return "", fmt.Errorf("project: %v", err)
	}
	dir = abs
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("project: not a directory: %s", args)
	}
	id, err := e.NewSession(ctx, dir)
	if err != nil {
		return "", err
	}
	return "project: session " + id + "\nThe session's workspace is " + dir + ".", nil
}
