package bash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/tool"
	"github.com/mrsirg97-rgb/rig/v2/tool/execwrap"
	"golang.org/x/sys/unix"
)

const outputCap = 256 * 1024

type Bash interface {
	tool.Definition
	Exec(ctx context.Context, args json.RawMessage) (string, error)

	Run(ctx context.Context, command, workspace string) (string, error)
}

type bashTool struct{ tool.Definition }

func New() Bash { return &bashTool{tool.Def("bash")} }

type args struct {
	Command   string `json:"command"`
	Workspace string `json:"workspace,omitempty"`
}

func (t bashTool) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var a args
	if err := tool.Decode(data, &a); err != nil {
		return "", fmt.Errorf("bash: args: %w", err)
	}
	return t.Run(ctx, a.Command, a.Workspace)
}

func (bashTool) Run(ctx context.Context, command, workspace string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("bash: empty command")
	}

	argv := execwrap.Args([]string{"bash", "-c", command})
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if workspace != "" {
		if err := checkCwd(workspace); err != nil {
			return "", fmt.Errorf("bash: workspace %s: %v", workspace, err)
		}
		cmd.Dir = workspace
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
		dir := workspace
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
