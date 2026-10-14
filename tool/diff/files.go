package diff

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const capLines = 100

func Files(ctx context.Context, ref string, paths []string) (string, error) {
	if ref == "" {
		ref = "HEAD"
	}
	cmdArgs := []string{"diff", "--no-color", "--no-ext-diff", "-U3", ref}
	if len(paths) > 0 {
		cmdArgs = append(cmdArgs, "--")
		cmdArgs = append(cmdArgs, paths...)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("diff files: cwd: %v", err)
	}
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	capture := &lineCap{}
	cmd.Stdout = capture
	var errb bytes.Buffer
	cmd.Stderr = &errb
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		stderr := errb.String()
		if strings.Contains(strings.ToLower(stderr), "not a git repository") {
			return "", fmt.Errorf("diff files: not a git repository (workspace %s)", cwd)
		}
		first := firstLine(stderr)
		if first == "" {
			first = err.Error()
		}
		return "", fmt.Errorf("diff files: %s", first)
	}
	return capture.reply(), nil
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

type lineCap struct {
	kept  bytes.Buffer
	lines int
	tail  bool
}

func (c *lineCap) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		line := p
		if i >= 0 {
			line = p[:i+1]
		}
		if c.lines < capLines {
			c.kept.Write(line)
		}
		if i < 0 {
			c.tail = true
			break
		}
		c.lines++
		c.tail = false
		p = p[i+1:]
	}
	return n, nil
}

func (c *lineCap) reply() string {
	total := c.lines
	if c.tail {
		total++
	}
	if total == 0 || (total == 1 && c.kept.Len() == 1 && c.kept.Bytes()[0] == '\n') {
		return "no changes"
	}
	kept := c.kept.String()
	if total <= capLines {
		return strings.TrimSuffix(kept, "\n")
	}
	cut := -1
	for n := 0; n < capLines-1; n++ {
		cut += strings.IndexByte(kept[cut+1:], '\n') + 1
	}
	return kept[:cut] + "\n… " + strconv.Itoa(total-(capLines-1)) + " more lines"
}
