package delegate

import (
	"context"
	"sort"
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/tool"
)

type workerState struct {
	n         int
	task      string
	heartbeat time.Time
	state     string
	tool      string
	toolAt    time.Time
}

func (a *adapter) running(member broadcast.Member, n int, task string) {
	if member == nil {
		return
	}
	a.mu.Lock()
	w, ok := a.workers[member.Id()]
	if !ok {
		w = workerState{n: n, task: tool.FirstLine(task)}
	}
	w.state, w.heartbeat = "running", time.Now()
	a.workers[member.Id()] = w
	a.mu.Unlock()
	a.emit()
}

func (a *adapter) track(member broadcast.Member, n int, task, state string) {
	if member == nil {
		return
	}
	a.mu.Lock()
	if _, ok := a.workers[member.Id()]; ok {
		a.mu.Unlock()
		return
	}
	a.workers[member.Id()] = workerState{n: n, task: tool.FirstLine(task), heartbeat: time.Now(), state: state}
	a.mu.Unlock()
	a.emit()
}

func (a *adapter) receive(err error, messages ...broadcast.Message) {
	if err != nil {
		return
	}
	changed := false
	a.mu.Lock()
	for _, m := range messages {
		w, ok := a.workers[m.Origin()]
		if !ok {
			continue
		}
		switch ev := m.Signal().(type) {
		case nil:
			w.heartbeat = time.Now()
		case core.ToolStart:
			w.heartbeat, w.tool, w.toolAt = time.Now(), ev.BoundedCall(), time.Now()
		default:
			continue
		}
		a.workers[m.Origin()] = w
		changed = true
	}
	a.mu.Unlock()
	if changed {
		a.emit()
	}
}

func (a *adapter) emit() {
	a.member.Publish(context.Background(), func(error) {}, broadcast.NewMessage(rig.MemberDelegate, true, a.snapshot()))
}

func (a *adapter) snapshot() core.SwarmStatus {
	a.mu.Lock()
	ws := make([]core.SwarmWorker, 0, len(a.workers))
	for _, w := range a.workers {
		ws = append(ws, core.SwarmWorker{
			ID: w.n, Role: "delegate", Task: w.task, Heartbeat: w.heartbeat,
			State: w.state, Tool: w.tool, ToolAt: w.toolAt,
		})
	}
	a.mu.Unlock()
	sort.Slice(ws, func(i, j int) bool { return ws[i].ID < ws[j].ID })
	return core.SwarmStatus{Workers: ws}
}
