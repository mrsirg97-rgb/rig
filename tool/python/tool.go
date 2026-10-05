package python

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/tool"
)

//go:embed kernel_host.py
var kernelHostSrc string

const (
	defaultTimeoutMs = 120_000
	minTimeoutMs     = 1_000
	maxTimeoutMs     = 600_000
	stderrTailLen    = 4096
	waitDelay        = 2 * time.Second
)

type Reply struct {
	ID     *string `json:"id"`
	Ok     bool    `json:"ok"`
	Out    *string `json:"out"`
	Err    *string `json:"err"`
	Result *string `json:"result"`
	Error  *string `json:"error"`
	Note   *string `json:"note"`
}

type request struct {
	Code *string `json:"code,omitempty"`
	Cmd  *string `json:"cmd,omitempty"`
	ID   string  `json:"id"`
}

type given struct {
	Code      *string `json:"code"`
	Action    string  `json:"action"`
	TimeoutMs *int    `json:"timeoutMs"`
}

type Python interface {
	tool.Definition
	Exec(ctx context.Context, args json.RawMessage) (string, error)

	Code(ctx context.Context, code string, timeoutMs int) (string, error)
	Vars(ctx context.Context, timeoutMs int) (string, error)
	Reset(ctx context.Context, timeoutMs int) (string, error)

	Run(ctx context.Context, code string, timeoutMs int) (Reply, error)
	Host() string
	Close()
}

type pyTool struct {
	tool.Definition
	k *kernel
}

var _ Python = (*pyTool)(nil)

func New(cwd ...string) Python {
	return &pyTool{Definition: tool.Def("python"), k: &kernel{python: defaultInterpreter(), host: DefaultHost(), queue: make(chan struct{}, 1), cwd: firstCwd(cwd)}}
}

func NewWith(python, host string, cwd ...string) Python {
	return &pyTool{Definition: tool.Def("python"), k: &kernel{python: python, host: host, queue: make(chan struct{}, 1), noBootstrap: true, cwd: firstCwd(cwd)}}
}

func firstCwd(cwd []string) string {
	if len(cwd) > 0 {
		return cwd[0]
	}
	return ""
}

func (t *pyTool) Host() string { return t.k.host }

func (t *pyTool) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var a given
	if err := json.Unmarshal(data, &a); err != nil {
		return "", fmt.Errorf("python: %v", err)
	}
	timeoutMs := defaultTimeoutMs
	if a.TimeoutMs != nil {
		timeoutMs = *a.TimeoutMs
	}
	switch a.Action {
	case "", "code":
		code := ""
		if a.Code != nil {
			code = *a.Code
		}
		return t.Code(ctx, code, timeoutMs)
	case "vars":
		return t.Vars(ctx, timeoutMs)
	case "reset":
		return t.Reset(ctx, timeoutMs)
	default:
		msg := fmt.Sprintf("python: unknown action %q; the actions are code (or omit it), vars, reset", a.Action)
		return msg, errors.New(msg)
	}
}

func (t *pyTool) Code(ctx context.Context, code string, timeoutMs int) (string, error) {
	if strings.TrimSpace(code) == "" {
		return "no code supplied", errors.New("no code supplied")
	}
	return t.run(ctx, request{Code: &code}, timeoutMs)
}

func (t *pyTool) Vars(ctx context.Context, timeoutMs int) (string, error) {
	return t.run(ctx, request{Cmd: strPtr("vars")}, timeoutMs)
}

func (t *pyTool) Reset(ctx context.Context, timeoutMs int) (string, error) {
	return t.run(ctx, request{Cmd: strPtr("reset")}, timeoutMs)
}

func (t *pyTool) run(ctx context.Context, req request, timeoutMs int) (string, error) {
	if timeoutMs < minTimeoutMs || timeoutMs > maxTimeoutMs {
		return "", fmt.Errorf("python: timeoutMs must be between %d and %d, got %d", minTimeoutMs, maxTimeoutMs, timeoutMs)
	}
	reply, err := t.k.send(ctx, req, timeoutMs)
	if err != nil {
		return "", err
	}
	text := render(reply)
	if reply.Ok {
		return text, nil
	}
	if reply.Error != nil && *reply.Error != "" {
		return text, errors.New(*reply.Error)
	}
	return text, errors.New(text)
}

func (t *pyTool) Close() {
	p := t.k.shutdown()
	if p != nil {
		select {
		case <-p.dead:
		case <-time.After(waitDelay + time.Second):
		}
	}
}

func (t *pyTool) Run(ctx context.Context, code string, timeoutMs int) (Reply, error) {
	req := request{Code: &code}
	return t.k.send(ctx, req, timeoutMs)
}
