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

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/tool/execwrap"
	"golang.org/x/sys/unix"
)

const outputCap = 256 * 1024

type tool struct{}

func New() core.Tool { return &tool{} }

func (tool) Name() string { return "bash" }

func (tool) Description() string {
	return "Runs a bash command in the session's workspace. Guidelines: use it for shell work, builds, git, and any CLI. To read a file you may edit, use read instead, so the edit that follows has something to check against; for a computation, use python. Reply: stdout and stderr together, in order, capped with a [TRUNCATED] marker that names the full size."
}

func (tool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command":   {"type": "string", "description": "the command line to run under bash(1)"},
			"workspace": {"type": "string", "description": "the workspace the job runs in"}
		},
		"required": ["command"]
	}`)
}

type args struct {
	Command   string `json:"command"`
	Workspace string `json:"workspace,omitempty"`
}

func (tool) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var a args
	if err := strictDecode(data, &a); err != nil {
		return "", fmt.Errorf("bash: args: %w", err)
	}
	if strings.TrimSpace(a.Command) == "" {
		return "", errors.New("bash: empty command")
	}

	argv := execwrap.Args([]string{"bash", "-c", a.Command})
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if a.Workspace != "" {
		if err := checkCwd(a.Workspace); err != nil {
			return "", fmt.Errorf("bash: workspace %s: %v", a.Workspace, err)
		}
		cmd.Dir = a.Workspace
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	out := newBounded(outputCap)
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()

	content := out.String()
	if err != nil {
		dir := a.Workspace
		if dir == "" {
			if d, werr := os.Getwd(); werr == nil {
				dir = d
			}
		}
		withCwd := content
		switch {
		case withCwd == "":
			withCwd = "(workspace " + dir + ")"
		case strings.HasSuffix(withCwd, "\n"):
			withCwd += "(workspace " + dir + ")"
		default:
			withCwd += "\n(workspace " + dir + ")"
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

func checkCwd(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return reasonOf(err)
	}
	if !info.IsDir() {
		return errors.New("not a directory")
	}
	if err := unix.Access(dir, unix.X_OK); err != nil {
		return reasonOf(err)
	}
	return nil
}

func reasonOf(err error) error {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errors.New(errno.Error())
	}
	return err
}

type bounded struct {
	cap       int
	buf       []byte
	truncated bool
}

func newBounded(cap int) *bounded { return &bounded{cap: cap} }

func (b *bounded) Write(p []byte) (int, error) {
	if room := b.cap - len(b.buf); room > 0 {
		if len(p) > room {
			b.buf = append(b.buf, p[:room]...)
			b.truncated = true
		} else {
			b.buf = append(b.buf, p...)
		}
	} else {
		b.truncated = true
	}
	return len(p), nil
}

func (b *bounded) String() string {
	s := string(b.buf)
	if b.truncated {
		s += "\n[output truncated]"
	}
	return s
}
