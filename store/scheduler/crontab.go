package scheduler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var tagRe = regexp.MustCompile(`^(?P<lead>\S.*?)\s+#\s*pane-scheduler:(?P<key>\S+)$`)

func LineFor(key, cron, runnerCmd string) string {
	return fmt.Sprintf("%s %s %s  # pane-scheduler:%s", cron, runnerCmd, key, key)
}

func Normalize(text string) string {
	trimmed := strings.TrimRight(text, "\n")
	if trimmed == "" {
		return ""
	}
	return trimmed + "\n"
}

type TaggedLine struct {
	Key    string
	Cron   string
	Paused bool
}

func Scan(text string) []TaggedLine {
	var out []TaggedLine
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t")
		if line == "" {
			continue
		}
		m := tagRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		body := line
		paused := false
		if strings.HasPrefix(line, "# ") {
			body = line[2:]
			paused = true
		}
		fields := strings.Fields(body)
		cron := ""
		if len(fields) >= 5 {
			cron = strings.Join(fields[:5], " ")
		}
		out = append(out, TaggedLine{Key: m[2], Cron: cron, Paused: paused})
	}
	return out
}

func linesOf(text string) []string {
	norm := Normalize(text)
	if norm == "" {
		return nil
	}
	return strings.Split(norm[:len(norm)-1], "\n")
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func findTagIndex(lines []string, key string) int {
	for i, l := range lines {
		m := tagRe.FindStringSubmatch(strings.TrimRight(l, " \t"))
		if m != nil && m[2] == key {
			return i
		}
	}
	return -1
}

func UpsertLine(text, key, cron, runnerCmd string) (string, bool) {
	lines := linesOf(text)
	line := LineFor(key, cron, runnerCmd)
	idx := findTagIndex(lines, key)
	if idx == -1 {
		lines = append(lines, line)
		return joinLines(lines), true
	}
	lines[idx] = line
	return joinLines(lines), false
}

func SetPaused(text, key string, paused bool) (string, bool) {
	lines := linesOf(text)
	idx := findTagIndex(lines, key)
	if idx == -1 {
		return joinLines(lines), false
	}
	active := lines[idx]
	if strings.HasPrefix(active, "# ") {
		active = active[2:]
	}
	next := active
	if paused {
		next = "# " + active
	}
	if next != lines[idx] {
		lines[idx] = next
	}
	return joinLines(lines), true
}

func RemoveLine(text, key string) (string, bool) {
	lines := linesOf(text)
	idx := findTagIndex(lines, key)
	if idx == -1 {
		return joinLines(lines), false
	}
	lines = append(lines[:idx], lines[idx+1:]...)
	return joinLines(lines), true
}

type Crontab interface {
	List() (string, error)
	Install(text string) error
}

var noCrontabRe = regexp.MustCompile(`(?i)no crontab for`)

const crontabTimeout = 5 * time.Second

func RealCrontab(bin string) Crontab {
	if bin == "" {
		bin = "crontab"
	}
	return realCrontab{bin: bin}
}

type realCrontab struct{ bin string }

func (r realCrontab) List() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), crontabTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, r.bin, "-l").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			stderr := strings.TrimSpace(string(exit.Stderr))
			if exit.ExitCode() == 1 && noCrontabRe.MatchString(stderr) {
				return "", nil
			}
			return "", fmt.Errorf("crontab list failed (exit %d): %s", exit.ExitCode(), stderrOf(stderr, err))
		}

		return "", errors.New("crontab: binary not found")
	}
	return string(out), nil
}

func (r realCrontab) Install(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), crontabTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.bin, "-")
	var stderr bytes.Buffer
	cmd.Stdin = strings.NewReader(text)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("crontab install failed (exit %d): %s", exit.ExitCode(), stderrOf(strings.TrimSpace(stderr.String()), err))
		}

		return errors.New("crontab: binary not found")
	}
	return nil
}

func stderrOf(stderr string, err error) string {
	if stderr != "" {
		return stderr
	}
	return err.Error()
}
