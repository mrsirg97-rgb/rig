package room

import (
	"context"
	"net"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
)

const (
	meshRounds = 3
	meshDial   = time.Second
	meshIdle   = 30 * time.Second
	meshPoll   = 100 * time.Millisecond
)

type Door interface {
	Id() int64
	Close()
	Machines() []string
	Horizon() int64
}

type repost struct {
	claim    broadcast.Mint
	priority int
	admitted bool
	released bool
}

type door struct {
	member   Member
	mesh     broadcast.Mesh
	engine   evt.Engine
	cancel   context.CancelFunc
	remote   []int64
	home     map[int64]bool
	out      map[int64]map[broadcast.Mint]broadcast.Act
	in       map[int64]map[broadcast.Mint]*repost
	machines map[broadcast.Mint]string
}

func NewDoor(member Member, cfg broadcast.MeshConfig, remote, home []int64) (Door, error) {
	engine := evt.NewEngine()
	ctx, cancel := context.WithCancel(context.Background())
	d := &door{
		member:   member,
		engine:   engine,
		cancel:   cancel,
		remote:   remote,
		home:     make(map[int64]bool, len(home)),
		out:      make(map[int64]map[broadcast.Mint]broadcast.Act),
		in:       make(map[int64]map[broadcast.Mint]*repost),
		machines: make(map[broadcast.Mint]string),
	}
	for _, key := range home {
		d.home[key] = true
	}
	cfg.Voice = PriorityFleet
	cfg.Rounds = meshRounds
	cfg.Timeout = meshDial
	cfg.Idle = meshIdle
	cfg.Told = d.told
	probe, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		cancel()
		return nil, err
	}
	probe.Close()
	d.mesh = broadcast.NewMesh(member.Id(), cfg)
	go func() { _ = d.mesh.Start(ctx) }()
	go engine.Start(ctx)
	go d.poll(ctx)
	return d, nil
}

func (d *door) Id() int64 { return d.member.Id() }

func (d *door) Machines() []string { return d.mesh.Machines() }

func (d *door) Horizon() int64 { return d.mesh.Horizon() }

func (d *door) Close() {
	d.cancel()
	d.mesh.Stop()
}

func (d *door) poll(ctx context.Context) {
	ticker := time.NewTicker(meshPoll)
	defer ticker.Stop()
	last := int64(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h := d.mesh.Horizon()
			if h == last {
				continue
			}
			last = h
			d.engine.Add(evt.Func(func(context.Context) { d.sweep() }), PriorityFleet)
		}
	}
}

func (d *door) told(act broadcast.Act) {
	d.engine.Add(evt.Func(func(context.Context) { d.absorb(act) }), PriorityFleet)
}

func (d *door) absorb(act broadcast.Act) {
	switch act.Kind {
	case broadcast.ActClaim:
		if !d.home[act.Key] {
			return
		}
		if d.in[act.Key] == nil {
			d.in[act.Key] = make(map[broadcast.Mint]*repost)
		}
		if _, ok := d.in[act.Key][act.Mint]; ok {
			return
		}
		d.in[act.Key][act.Mint] = &repost{priority: act.Priority}
		d.machines[act.Mint] = act.Machine
		d.member.Claim(act.Key, act.Priority, 0, d.entered(act.Key, act.Mint))
	case broadcast.ActRelease:
		if act.Claim == nil {
			return
		}
		r, ok := d.in[act.Claim.Key][act.Claim.Mint]
		if !ok {
			return
		}
		if r.admitted {
			d.member.Release(r.claim, act.Priority)
			d.forget(act.Claim.Key, act.Claim.Mint)
			return
		}
		r.released = true
	}
}

func (d *door) entered(key int64, origin broadcast.Mint) func(broadcast.Mint, bool) {
	return func(claim broadcast.Mint, admitted bool) {
		if !admitted {
			return
		}
		d.engine.Add(evt.Func(func(context.Context) {
			r, ok := d.in[key][origin]
			if !ok {
				return
			}
			r.claim = claim
			r.admitted = true
			if r.released {
				d.member.Release(claim, r.priority)
				d.forget(key, origin)
			}
		}), PriorityFleet)
	}
}

func (d *door) forget(key int64, mint broadcast.Mint) {
	delete(d.in[key], mint)
	delete(d.machines, mint)
}

func (d *door) sweep() {
	for _, key := range d.remote {
		holders := d.member.Holders(key)
		live := make(map[broadcast.Mint]bool, len(holders))
		for _, op := range holders {
			live[op.Mint()] = true
			if _, ok := d.out[key][op.Mint()]; ok {
				continue
			}
			act, ok := d.mesh.Post(key, op.Priority())
			if !ok {
				continue
			}
			if d.out[key] == nil {
				d.out[key] = make(map[broadcast.Mint]broadcast.Act)
			}
			d.out[key][op.Mint()] = act
		}
		for mint, act := range d.out[key] {
			if live[mint] {
				continue
			}
			if d.mesh.Release(act) {
				delete(d.out[key], mint)
			}
		}
	}
	for key := range d.home {
		held := make(map[broadcast.Mint]bool)
		for _, act := range d.mesh.Holders(key) {
			held[act.Mint] = true
		}
		for mint, r := range d.in[key] {
			if held[mint] {
				continue
			}
			if r.released {
				continue
			}
			if r.admitted {
				d.member.Release(r.claim, r.priority)
				d.forget(key, mint)
				continue
			}
			r.released = true
		}
	}
}
