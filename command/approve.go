package command

import (
	"context"
	"errors"
	"strings"
)

type approveCmd struct{}

func (approveCmd) Sub() []Sub {
	return []Sub{
		{Name: "auto", Desc: "tools run unasked — today's behavior"},
		{Name: "manual", Desc: "every mutating tool call pauses for y/n (esc declines and interrupts)"},
	}
}

func (approveCmd) Name() string { return "approve" }

func (approveCmd) Description() string {
	return "the tool-approval dial: auto or manual (manual pauses every mutating call for y/n)"
}

func (approveCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if e.Approve == nil {
		return "", errors.New("approve: no approve seam (the root did not wire one)")
	}
	fields := strings.Fields(args)
	if len(fields) > 1 {
		return "", errors.New("approve: usage: approve [auto|manual]")
	}
	if len(fields) == 1 {
		if e.SetApprove == nil {
			return "", errors.New("approve: no set seam (the root did not wire one)")
		}
		if err := e.SetApprove(ctx, fields[0]); err != nil {
			return "", err
		}
		return "approve: " + fields[0] + " (the next tool call)", nil
	}
	label := e.Approve()
	if label == "" {
		label = "auto"
	}
	return "approve: " + label, nil
}
