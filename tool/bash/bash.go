package bash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
)

const outputCap = 256 * 1024

type tool struct{}

func New() core.Tool { return &tool{} }

func (tool) Name() string { return "bash" }

func (tool) Description() string {
	return "run a bash(1) command; captures combined output (capped)"
}

func (tool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {"type": "string", "description": "the command line to run under bash(1)"},
			"cwd":     {"type": "string", "description": "working directory for the command"}
		},
		"required": ["command"]
	}`)
}

type args struct {
	Command string `json:"command"`
	Cwd     string `json:"cwd,omitempty"`
}

func (tool) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var a args
	if err := strictDecode(data, &a); err != nil {
		return "", fmt.Errorf("bash: args: %w", err)
	}
	if strings.TrimSpace(a.Command) == "" {
		return "", errors.New("bash: empty command")
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", a.Command)
	if a.Cwd != "" {
		cmd.Dir = a.Cwd
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()

	content := out.String()
	if len(content) > outputCap {
		content = content[:outputCap] + "\n[output truncated]"
	}
	if err != nil {
		dir := a.Cwd
		if dir == "" {
			if d, werr := os.Getwd(); werr == nil {
				dir = d
			}
		}
		withCwd := content
		switch {
		case withCwd == "":
			withCwd = "(cwd " + dir + ")"
		case strings.HasSuffix(withCwd, "\n"):
			withCwd += "(cwd " + dir + ")"
		default:
			withCwd += "\n(cwd " + dir + ")"
		}
		if ctx.Err() != nil {
			return withCwd, ctx.Err()
		}

		if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
			return content, nil
		}

		return withCwd, fmt.Errorf("bash: %w", err)
	}
	return content, nil
}

func strictDecode(data json.RawMessage, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}
