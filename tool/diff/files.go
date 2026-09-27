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
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		stderr := errb.String()
		if strings.Contains(strings.ToLower(stderr), "not a git repository") {
			return "", fmt.Errorf("diff files: not a git repository (cwd %s)", cwd)
		}
		first := firstLine(stderr)
		if first == "" {
			first = err.Error()
		}
		return "", fmt.Errorf("diff files: %s", first)
	}
	body := strings.TrimSuffix(out.String(), "\n")
	if body == "" {
		return "no changes", nil
	}
	return capBody(body), nil
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func capBody(body string) string {
	ln := strings.Split(body, "\n")
	if len(ln) <= capLines {
		return body
	}
	k := len(ln) - (capLines - 1)
	return strings.Join(ln[:capLines-1], "\n") + "\n… " + strconv.Itoa(k) + " more lines"
}
