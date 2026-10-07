package scheduler

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"syscall"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
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

func Fleet() (broadcast.Transport, error) {
	v, ok := os.LookupEnv(FleetEnv)
	if !ok {
		return nil, nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%s=%q: the child's member id is an integer", FleetEnv, v)
	}
	syscall.CloseOnExec(FleetFD)
	return broadcast.NewPipeTransport(id, os.NewFile(FleetFD, "fleet"), broadcast.NewJSONEncoder()), nil
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
	p := &fleetPipe{ctx: ctx, id: id, w: w, done: make(chan struct{})}
	p.transport = broadcast.NewPipeTransport(id, r, broadcast.NewJSONEncoder())
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

// into carries the fleet end onto a context the caller owns, so a worker that
// outlives the call that started it can be bound to a context of its own.
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
