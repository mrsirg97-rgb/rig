package python

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
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

type Tool struct {
	tool.Definition
	k *kernel
}

var _ core.Tool = (*Tool)(nil)

func New() *Tool {
	return &Tool{Definition: tool.Def("python"), k: &kernel{python: defaultInterpreter(), host: DefaultHost(), queue: make(chan struct{}, 1)}}
}

func NewWith(python, host string) *Tool {
	return &Tool{Definition: tool.Def("python"), k: &kernel{python: python, host: host, queue: make(chan struct{}, 1), noBootstrap: true}}
}

func (t *Tool) Host() string { return t.k.host }

func (t *Tool) SetCwd(cwd string) { t.k.cwd = cwd }

func (t *Tool) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var a given
	if err := json.Unmarshal(data, &a); err != nil {
		return "", fmt.Errorf("python: %v", err)
	}
	var req request
	switch a.Action {
	case "", "code":
		if a.Code == nil || strings.TrimSpace(*a.Code) == "" {
			return "no code supplied", errors.New("no code supplied")
		}
		req.Code = a.Code
	case "vars", "reset":
		req.Cmd = &a.Action
	default:
		msg := fmt.Sprintf("python: unknown action %q; the actions are code (or omit it), vars, reset", a.Action)
		return msg, errors.New(msg)
	}
	timeoutMs := defaultTimeoutMs
	if a.TimeoutMs != nil {
		timeoutMs = *a.TimeoutMs
	}
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

func (t *Tool) Close() {
	p := t.k.shutdown()
	if p != nil {
		select {
		case <-p.dead:
		case <-time.After(waitDelay + time.Second):
		}
	}
}

func (t *Tool) Run(ctx context.Context, code string, timeoutMs int) (Reply, error) {
	req := request{Code: &code}
	return t.k.send(ctx, req, timeoutMs)
}
