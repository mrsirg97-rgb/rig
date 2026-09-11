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
	"golang.org/x/sys/unix"
)

const outputCap = 256 * 1024

type tool struct{}

func New() core.Tool { return &tool{} }

func (tool) Name() string { return "bash" }

func (tool) Description() string {
	return "run a bash(1) command. Guidelines: shell work, builds, git, any CLI; reading a file -> read, a computation -> python. Reply: combined stdout and stderr, capped with a [TRUNCATED] marker naming the full size."
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
		if err := checkCwd(a.Cwd); err != nil {
			return fmt.Sprintf("bash: cwd %s: %v", a.Cwd, err), nil
		}
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
	out := newBounded(outputCap)
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()

	content := out.String()
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

// bounded keeps the head of a child's output at cap and drops the rest,
// so a huge stream cannot pin memory: the child's writes are always fully
// consumed (it never blocks) and the kept output is byte-identical to the
// post-hoc truncation (head + marker).
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
