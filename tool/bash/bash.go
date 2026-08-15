// Package bash is the bash(1) tool: real subprocesses, output surfaced to
// the model, bounded.
package bash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/mrsirg97-rgb/looper/core"
)

// outputCap bounds the work one call can induce on the transcript.
const outputCap = 256 * 1024

type tool struct{}

func New() core.Tool { return &tool{} }

func (tool) Name() string { return "bash" }

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
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()

	content := out.String()
	if len(content) > outputCap {
		content = content[:outputCap] + "\n[output truncated]"
	}
	if err != nil {
		if ctx.Err() != nil {
			return content, ctx.Err()
		}
		return content, fmt.Errorf("bash: %w", err)
	}
	return content, nil
}

func strictDecode(data json.RawMessage, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}
