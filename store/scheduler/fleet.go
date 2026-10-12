package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"syscall"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/core"
)

const (
	FleetEnv     = "RIG_FLEET"
	FleetFD      = 3
	runnerOrigin = 0
)

type fleetKey struct{}

type fleetEnd struct {
	id   int64
	file *os.File
}

func WithFleet(ctx context.Context, id int64, w *os.File) context.Context {
	return context.WithValue(ctx, fleetKey{}, fleetEnd{id: id, file: w})
}

func FleetFrom(ctx context.Context) (int64, *os.File, bool) {
	end, ok := ctx.Value(fleetKey{}).(fleetEnd)
	return end.id, end.file, ok
}

func Wire() (broadcast.Registry, error) {
	reg, err := broadcast.Declare(broadcast.NewRegistry(), "notice",
		func(n evt.Notice) ([]byte, error) { return json.Marshal(n) },
		func(b []byte) (evt.Notice, error) {
			var n evt.Notice
			return n, json.Unmarshal(b, &n)
		})
	if err != nil {
		return nil, err
	}
	reg, err = broadcast.Declare(reg, "swarm_status",
		func(s core.SwarmStatus) ([]byte, error) { return json.Marshal(s) },
		func(b []byte) (core.SwarmStatus, error) {
			var s core.SwarmStatus
			return s, json.Unmarshal(b, &s)
		})
	if err != nil {
		return nil, err
	}
	reg, err = broadcast.Declare(reg, "verdict",
		func(v core.Verdict) ([]byte, error) { return json.Marshal(v) },
		func(b []byte) (core.Verdict, error) {
			var v core.Verdict
			return v, json.Unmarshal(b, &v)
		})
	if err != nil {
		return nil, err
	}
	reg, err = broadcast.Declare(reg, "phase",
		func(p core.Phase) ([]byte, error) { return json.Marshal(p) },
		func(b []byte) (core.Phase, error) {
			var p core.Phase
			return p, json.Unmarshal(b, &p)
		})
	if err != nil {
		return nil, err
	}
	reg, err = broadcast.Declare(reg, "reasoning",
		func(r core.ReasoningDelta) ([]byte, error) { return json.Marshal(r) },
		func(b []byte) (core.ReasoningDelta, error) {
			var r core.ReasoningDelta
			return r, json.Unmarshal(b, &r)
		})
	if err != nil {
		return nil, err
	}
	reg, err = broadcast.Declare(reg, "tool_start",
		func(t core.ToolStart) ([]byte, error) { return json.Marshal(t) },
		func(b []byte) (core.ToolStart, error) {
			var t core.ToolStart
			return t, json.Unmarshal(b, &t)
		})
	if err != nil {
		return nil, err
	}
	return reg, nil
}

func Fleet() (broadcast.Transport, error) {
	v, ok := os.LookupEnv(FleetEnv)
	if !ok {
		return nil, nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%s=%q: the child's member id is an integer", FleetEnv, v)
	}
	reg, err := Wire()
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(FleetFD)
	return broadcast.NewPipeTransport(id, os.NewFile(FleetFD, "fleet"), broadcast.NewJSONEncoder(reg)), nil
}

type fleetPipe struct {
	ctx       context.Context
	id        int64
	w         *os.File
	transport broadcast.Transport
	done      chan struct{}
}

func openFleet(ctx context.Context, id int64, receive func(messages ...broadcast.Message)) (*fleetPipe, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	reg, err := Wire()
	if err != nil {
		return nil, err
	}
	p := &fleetPipe{ctx: ctx, id: id, w: w, done: make(chan struct{})}
	p.transport = broadcast.NewPipeTransport(id, r, broadcast.NewJSONEncoder(reg))
	p.transport.Recv(ctx, func(err error, messages ...broadcast.Message) {
		if err != nil {
			close(p.done)
			return
		}
		receive(messages...)
	})
	return p, nil
}

func (p *fleetPipe) spawnCtx() context.Context {
	return WithFleet(p.ctx, p.id, p.w)
}

func (p *fleetPipe) into(ctx context.Context) context.Context {
	return WithFleet(ctx, p.id, p.w)
}

func (p *fleetPipe) env() string {
	return FleetEnv + "=" + strconv.FormatInt(p.id, 10)
}

func (p *fleetPipe) close() {
	p.w.Close()
	<-p.done
	p.transport.Close()
}
