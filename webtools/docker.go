package webtools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const maxRunBytes = 64 * 1024

type Runner interface {
	Run(ctx context.Context, name string, argv ...string) (string, error)
}

func NewRunner() Runner { return &runner{} }

type runner struct{}

func (runner) Run(ctx context.Context, name string, argv ...string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, path, argv...)
	t := &tail{}
	cmd.Stdout = t
	cmd.Stderr = t
	runErr := cmd.Run()
	out := t.String()
	if runErr != nil {
		if voice := strings.TrimSpace(out); voice != "" {
			return out, fmt.Errorf("%s %s: %v: %s", name, strings.Join(argv, " "), runErr, voice)
		}
		return out, fmt.Errorf("%s %s: %v", name, strings.Join(argv, " "), runErr)
	}
	return out, nil
}

type tail struct {
	buf     []byte
	dropped int
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - 2*maxRunBytes; over > 0 {
		t.buf = t.buf[over:]
		t.dropped += over
	}
	return len(p), nil
}

func (t *tail) String() string {
	if len(t.buf) <= maxRunBytes {
		return dropNote(t.dropped) + string(t.buf)
	}
	over := len(t.buf) - maxRunBytes
	return dropNote(t.dropped+over) + string(t.buf[over:])
}

func dropNote(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("[TRUNCATED: %d bytes of output dropped from the head]\n", n)
}

func capTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	over := len(s) - n
	return dropNote(over) + s[over:]
}

const statusMark = "exit status "

func voiceOf(err error) string {
	v := strings.TrimSpace(err.Error())
	i := strings.Index(v, statusMark)
	if i < 0 {
		return v
	}
	rest := v[i+len(statusMark):]
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	after := strings.TrimSpace(rest[digits:])
	if after == "" {
		return ""
	}
	if after[0] != ':' {
		return v
	}
	return strings.TrimSpace(after[1:])
}

func dockerVoice(err error, out string) string {
	voice := voiceOf(err)
	tail := strings.TrimSpace(out)
	if tail == "" || strings.Contains(voice, tail) {
		return voice
	}
	if voice == "" {
		return tail
	}
	return voice + ": " + tail
}
