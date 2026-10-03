package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func RealFetch(timeout time.Duration) Fetch {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	if Transport != nil {
		client.Transport = Transport
	}
	return func(url string) (json.RawMessage, error) {
		resp, err := client.Get(url)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var raw json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return nil, err
		}
		return raw, nil
	}
}

const PromptStdin = "-"

type promptKey struct{}

func WithPrompt(ctx context.Context, prompt string) context.Context {
	return context.WithValue(ctx, promptKey{}, prompt)
}

func PromptFrom(ctx context.Context) (string, bool) {
	p, ok := ctx.Value(promptKey{}).(string)
	return p, ok
}

func RealSpawn(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (SpawnResult, error) {
	if len(argv) == 0 {
		return SpawnResult{}, errors.New("spawn: empty argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if p, ok := PromptFrom(ctx); ok {
		cmd.Stdin = strings.NewReader(p)
	}
	cmd.Env = env
	cmd.SysProcAttr = spawnSysProcAttr()
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	out := newCapture(spawnCaptureCap)
	errBuf := newCapture(spawnCaptureCap)
	out.observe = observe
	errBuf.observe = observe
	cmd.Stdout = out
	cmd.Stderr = errBuf
	runErr := cmd.Run()
	res := SpawnResult{Stdout: out.String(), Stderr: errBuf.String()}
	switch {
	case runErr == nil:
		res.Exit = 0
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.TimedOut = true
		res.Exit = 1
		res.Stderr += "\n[runner: killed after timeout]\n"
	default:
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			res.Exit = exit.ExitCode()
			if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				res.Signal = ws.Signal()
			}
			return res, nil
		}

		return SpawnResult{}, fmt.Errorf("spawn: %w", runErr)
	}
	return res, nil
}

func spawnReason(ctx context.Context, res SpawnResult, stalled bool) string {
	switch {
	case res.TimedOut:
		return "killed after timeout"
	case stalled:
		return "killed after stall"
	case ctx.Err() != nil && res.Exit != 0:
		return "canceled"
	case res.Signal != 0:
		return fmt.Sprintf("killed by signal %d", res.Signal)
	}
	return ""
}

type capture struct {
	head    []byte
	tail    []byte
	headCap int
	tailCap int
	full    bool
	observe func([]byte)
}

func newCapture(cap int) *capture {
	if cap < 2 {
		cap = 2
	}
	half := cap / 2
	return &capture{head: make([]byte, 0, half), headCap: half, tailCap: half}
}

func (c *capture) Write(p []byte) (int, error) {
	if c.observe != nil {
		c.observe(p)
	}
	if len(c.head) < c.headCap {
		room := c.headCap - len(c.head)
		if len(p) <= room {
			c.head = append(c.head, p...)
			return len(p), nil
		}
		c.head = append(c.head, p[:room]...)
		p = p[room:]
	}
	drop := len(c.tail) + len(p) - c.tailCap
	if drop > 0 {
		c.full = true
		if drop >= len(c.tail) {
			c.tail = c.tail[:0]
		} else {
			c.tail = append(c.tail[:0], c.tail[drop:]...)
		}
	}
	kept := len(p)
	if kept > c.tailCap {
		kept = c.tailCap
	}
	if kept > 0 {
		c.tail = append(c.tail, p[len(p)-kept:]...)
	}
	return len(p), nil
}

func (c *capture) String() string {
	var b strings.Builder
	b.Write(c.head)
	b.Write(c.tail)
	if c.full {
		fmt.Fprintf(&b, "\n[spawn: output truncated, kept the first and last %d bytes of %d]\n", c.headCap, c.headCap+c.tailCap)
	}
	return b.String()
}
