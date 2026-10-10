package verdict

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/tool"
)

type Verdict interface {
	tool.Definition
	Exec(ctx context.Context, args json.RawMessage) (string, error)

	Deliver(ctx context.Context, row int64, accept bool, reason string) (string, error)
}

type adapter struct {
	tool.Definition
	fleet broadcast.Transport
}

func New(fleet broadcast.Transport) Verdict {
	if fleet == nil {
		panic("verdict: the tool speaks on the fleet pipe; the transport is a constructor argument")
	}
	return &adapter{Definition: tool.Def("verdict"), fleet: fleet}
}

type args struct {
	Row    int64  `json:"row,omitempty"`
	Accept bool   `json:"accept"`
	Reason string `json:"reason,omitempty"`
}

func (a *adapter) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var g args
	if err := tool.Decode(data, &g); err != nil {
		return "", fmt.Errorf("verdict: args: %w", err)
	}
	return a.Deliver(ctx, g.Row, g.Accept, g.Reason)
}

func (a *adapter) Deliver(ctx context.Context, row int64, accept bool, reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if !accept && reason == "" {
		return "", errors.New("verdict: a reject needs the reason (what the author must fix, or the corrected answer)")
	}
	var ack error
	a.fleet.Send(ctx, func(err error) { ack = err }, broadcast.NewMessage(a.fleet.Id(), true, core.Verdict{Row: row, Accept: accept, Reason: reason}))
	if ack != nil {
		return "", fmt.Errorf("verdict: %w", ack)
	}
	return "recorded", nil
}
