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
	Holding(key int64, mint broadcast.Mint) (bool, string, int)
	Foreign(mint broadcast.Mint) bool
	Refused(key int64, mint broadcast.Mint, since int64) bool
	Release(mint broadcast.Mint)
}

type repost struct {
	claim    broadcast.Mint
	priority int
	admitted bool
	released bool
	remote   broadcast.Mint
	machine  string
}

type door struct {
	member  Member
	mesh    broadcast.Mesh
	engine  evt.Engine
	cancel  context.CancelFunc
	gone    <-chan struct{}
	window  int64
	remote  []int64
	home    map[int64]bool
	foreign map[broadcast.Mint]broadcast.Act
	out     map[int64]map[broadcast.Mint]broadcast.Act
	in      map[int64]map[broadcast.Mint]*repost
}

func NewDoor(member Member, cfg broadcast.MeshConfig, remote, home []int64) (Door, error) {
	engine := evt.NewEngine()
	ctx, cancel := context.WithCancel(context.Background())
	d := &door{
		member:  member,
		engine:  engine,
		cancel:  cancel,
		gone:    ctx.Done(),
		window:  cfg.Window,
		remote:  remote,
		home:    make(map[int64]bool, len(home)),
		foreign: make(map[broadcast.Mint]broadcast.Act),
		out:     make(map[int64]map[broadcast.Mint]broadcast.Act),
		in:      make(map[int64]map[broadcast.Mint]*repost),
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
	d.mesh = broadcast.NewMesh(broadcast.MemberOf([]byte(cfg.Machine)), cfg)
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
			d.foreign[mirrored(act)] = act
			return
		}
		if d.in[act.Key] == nil {
			d.in[act.Key] = make(map[broadcast.Mint]*repost)
		}
		if _, ok := d.in[act.Key][act.Mint]; ok {
			return
		}
		d.in[act.Key][act.Mint] = &repost{priority: act.Priority, remote: mirrored(act), machine: act.Machine}
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
}

func (d *door) Release(mint broadcast.Mint) {
	d.engine.Add(evt.Func(func(context.Context) {
		for key, entries := range d.in {
			for actMint, r := range entries {
				if r.remote != mint {
					continue
				}
				if r.admitted {
					d.member.Release(r.claim, r.priority)
					d.forget(key, actMint)
				} else {
					r.released = true
				}
				return
			}
		}
	}), PriorityFleet)
}

func mirrored(act broadcast.Act) broadcast.Mint {
	if act.Claim == nil {
		return broadcast.Mint{}
	}
	return act.Claim.Mint
}

func (d *door) Holding(key int64, mint broadcast.Mint) (bool, string, int) {
	found := make(chan repost, 1)
	d.engine.Add(evt.Func(func(context.Context) {
		for _, r := range d.in[key] {
			if r.remote != mint {
				continue
			}
			found <- *r
			return
		}
		close(found)
	}), PriorityFleet)
	select {
	case r, ok := <-found:
		if !ok {
			return false, "", 0
		}
		return r.admitted, r.machine, r.priority
	case <-d.gone:
		return false, "", 0
	}
}

func (d *door) Foreign(mint broadcast.Mint) bool {
	seen := make(chan bool, 1)
	d.engine.Add(evt.Func(func(context.Context) {
		_, ok := d.foreign[mint]
		seen <- ok
	}), PriorityFleet)
	select {
	case ok := <-seen:
		return ok
	case <-d.gone:
		return false
	}
}

func (d *door) Refused(key int64, mint broadcast.Mint, since int64) bool {
	answer := make(chan bool, 1)
	d.engine.Add(evt.Func(func(context.Context) {
		for _, act := range d.mesh.Holders(key) {
			if act.Claim != nil && act.Claim.Mint == mint {
				answer <- false
				return
			}
		}
		answer <- d.mesh.Horizon() >= since+d.window+2
	}), PriorityFleet)
	select {
	case refused := <-answer:
		return refused
	case <-d.gone:
		return false
	}
}

func (d *door) sweep() {
	h := d.mesh.Horizon()
	for mint, act := range d.foreign {
		if h > int64(act.Tick())+d.window+2 {
			delete(d.foreign, mint)
		}
	}
	for _, key := range d.remote {
		holders := d.member.Holders(key)
		live := make(map[broadcast.Mint]bool, len(holders))
		for _, op := range holders {
			live[op.Mint()] = true
			if _, ok := d.out[key][op.Mint()]; ok {
				continue
			}
			act, ok := d.mesh.Post(key, op.Priority(), op.Mint())
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
