package verdict

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/tool"
)

type adapter struct {
	tool.Definition
	fleet broadcast.Transport
}

func New(fleet broadcast.Transport) core.Tool {
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
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return "", fmt.Errorf("verdict: args: %w", err)
	}
	g.Reason = strings.TrimSpace(g.Reason)
	if !g.Accept && g.Reason == "" {
		return "", errors.New("verdict: a reject needs the reason (what the author must fix, or the corrected answer)")
	}
	var ack error
	a.fleet.Send(ctx, func(err error) { ack = err }, broadcast.NewMessage(a.fleet.Id(), true, core.Verdict{Row: g.Row, Accept: g.Accept, Reason: g.Reason}))
	if ack != nil {
		return "", fmt.Errorf("verdict: %w", ack)
	}
	return "recorded", nil
}
