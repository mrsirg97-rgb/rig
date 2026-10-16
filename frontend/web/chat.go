package web

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3/command"
	"github.com/mrsirg97-rgb/rig/v3/core"
)

type chat struct {
	lines chan string
	slot  chan string
	wake  chan struct{}

	mu          sync.Mutex
	cancel      context.CancelFunc
	turnCtx     context.Context
	steeredLive bool

	dispatchMu sync.Mutex

	inbox []core.WorkerDone

	commands map[string]core.Command
	known    []string
	env      any

	hub  sync.Mutex
	subs map[chan []byte]struct{}
	ring []frame
	seq  int64
	last map[string]any
	cuts []cut

	asks   map[string]chan bool
	askSeq int64

	stopWorkers func()

	swarm    core.SwarmStatus
	delegate core.SwarmStatus
}

func newChat(cmds []core.Command, env any, stopWorkers func()) *chat {
	c := &chat{
		lines:       make(chan string, 1),
		slot:        make(chan string, 1),
		wake:        make(chan struct{}, 1),
		subs:        map[chan []byte]struct{}{},
		asks:        map[string]chan bool{},
		env:         env,
		stopWorkers: stopWorkers,
	}
	if len(cmds) > 0 {
		c.commands = make(map[string]core.Command, len(cmds))
		for _, cmd := range cmds {
			c.commands[cmd.Name()] = cmd
			c.known = append(c.known, cmd.Name())
		}
	}
	if e, ok := env.(*command.Env); ok {
		e.Steer = c
	}
	return c
}

func (c *chat) queueSlot(line string) {
	select {
	case c.slot <- line:
	default:
		select {
		case <-c.slot:
		default:
		}
		c.slot <- line
	}
}

func (c *chat) Input(ctx context.Context) (string, error) {
	c.mu.Lock()
	if cancel, ok := core.InterruptFrom(ctx); ok {
		c.cancel = cancel
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.mu.Unlock()
	}()
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if block, ok := c.drainInbox(); ok {
			c.mu.Lock()
			c.steeredLive = false
			c.turnCtx = ctx
			c.mu.Unlock()
			c.publish(map[string]any{"kind": "prompt", "text": block})
			return block, nil
		}
		select {
		case line := <-c.slot:
			if strings.TrimSpace(line) == "" {
				continue
			}
			if out, ok := c.take(ctx, line); ok {
				return out, nil
			}
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-c.wake:
		case line := <-c.lines:
			if strings.TrimSpace(line) == "" {
				continue
			}
			if out, ok := c.take(ctx, line); ok {
				return out, nil
			}
			continue
		}
	}
}

func (c *chat) take(ctx context.Context, line string) (string, bool) {
	if c.commands != nil && command.IsCommandLine(line) {
		c.dispatch(ctx, line)
		return "", false
	}
	if strings.HasPrefix(line, "//") {
		line = command.Unescape(line)
	}
	c.mu.Lock()
	c.turnCtx = ctx
	c.steeredLive = false
	c.mu.Unlock()
	c.publish(map[string]any{"kind": "prompt", "text": line})
	return line, true
}

func (c *chat) dispatch(ctx context.Context, line string) map[string]any {
	c.dispatchMu.Lock()
	defer c.dispatchMu.Unlock()
	name, args := command.Parse(line)
	f := map[string]any{"kind": "command", "name": name, "args": args}
	cmd, ok := c.commands[name]
	if !ok {
		f["err"] = "unknown command: " + name + " (known: " + strings.Join(c.known, ", ") + ")"
		c.publish(f)
		return f
	}
	out, err := cmd.Run(ctx, args, c.env)
	if err != nil {
		f["err"] = err.Error()
	} else {
		f["text"] = strings.TrimRight(out, "\n")
	}
	c.publish(f)
	return f
}

func (c *chat) Steer(text string) bool {
	c.queueSlot(text)
	c.mu.Lock()
	live := c.turnCtx != nil && c.turnCtx.Err() == nil
	wasLive := c.steeredLive
	c.steeredLive = false
	cancel := c.cancel
	c.mu.Unlock()
	if live && cancel != nil {
		cancel()
		return true
	}
	return wasLive
}

func (c *chat) Interrupt() bool {
	c.mu.Lock()
	live := c.turnCtx != nil && c.turnCtx.Err() == nil
	wasLive := c.steeredLive
	c.steeredLive = false
	cancel := c.cancel
	stop := c.stopWorkers
	c.mu.Unlock()
	if live && cancel != nil {
		cancel()
		return true
	}
	if !wasLive && stop != nil {
		stop()
		return true
	}
	return wasLive
}

func (c *chat) ClearSlot() {
	select {
	case <-c.slot:
	default:
	}
}

func (c *chat) LiveTurn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnCtx != nil && c.turnCtx.Err() == nil
}

func (c *chat) Ask(ctx context.Context, prompt string) bool {
	c.hub.Lock()
	c.askSeq++
	id := strconv.FormatInt(c.askSeq, 10)
	ch := make(chan bool, 1)
	c.asks[id] = ch
	c.hub.Unlock()
	c.publish(map[string]any{"kind": "ask", "id": id, "prompt": prompt})
	yes := false
	select {
	case yes = <-ch:
	case <-ctx.Done():
	}
	c.hub.Lock()
	delete(c.asks, id)
	c.hub.Unlock()
	c.publish(map[string]any{"kind": "ask_done", "id": id, "yes": yes})
	return yes
}

func (c *chat) answer(id string, yes bool) bool {
	c.hub.Lock()
	ch, ok := c.asks[id]
	c.hub.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- yes:
	default:
	}
	return true
}

func (c *chat) seqNow() int64 {
	c.hub.Lock()
	defer c.hub.Unlock()
	return c.seq
}

func usageOf(u core.Usage) map[string]any {
	return map[string]any{"prompt": u.Prompt, "completion": u.Completion, "cache_read": u.CacheRead, "cache_write": u.CacheWrite, "cost": u.Cost}
}

func (c *chat) Notify(ev evt.Signal) {
	switch e := ev.(type) {
	case core.TextDelta:
		c.publish(map[string]any{"kind": "text", "text": e.Text})
	case core.ReasoningDelta:
		c.publish(map[string]any{"kind": "reasoning", "text": e.Text})
	case core.ToolStart:
		c.publish(map[string]any{"kind": "tool_start", "id": e.Call.ID, "name": e.Call.Name, "args": string(e.Call.Args)})
	case core.ToolResult:
		f := map[string]any{"kind": "tool_result", "id": e.ID, "content": capResult(e.Content), "ms": e.Duration.Milliseconds()}
		if e.Err != nil {
			f["err"] = e.Err.Error()
		}
		c.publish(f)
	case core.Done:
		c.publish(map[string]any{"kind": "done", "usage": usageOf(e.Usage), "model": e.Model, "stop": e.StopReason})
	case core.EmptyTurn:
		c.publish(map[string]any{"kind": "empty_turn", "resample": e.Resample, "limit": e.Limit, "usage": usageOf(e.Usage)})
	case core.Compacting:
		c.publish(map[string]any{"kind": "compacting"})
	case core.Compacted:
		c.publish(map[string]any{"kind": "compacted", "dropped": e.Dropped, "kept": e.Kept, "usage": usageOf(e.Usage)})
	case core.Fault:
		c.publish(map[string]any{"kind": "fault", "text": e.Err.Error()})
	case core.TurnEnd:
		c.publish(map[string]any{"kind": "turn_end", "reason": string(e.Reason)})
	case core.SwarmStatus:
		c.hub.Lock()
		c.trackSwarm(e)
		frame := map[string]any{
			"kind": "swarm_status", "workers": swarmRows(c.swarm.Workers),
			"pending": c.swarm.Pending, "review": c.swarm.Review,
			"delegate": swarmRows(c.delegate.Workers),
		}
		c.hub.Unlock()
		c.publish(frame)
	case evt.Notice:
		c.publish(map[string]any{"kind": "notice", "source": e.Source, "text": e.Text, "level": e.Level.String()})
	case core.WorkerDone:
		c.mu.Lock()
		c.inbox = append(c.inbox, e)
		c.mu.Unlock()
		c.publish(map[string]any{
			"kind": "worker_done", "head": e.Head(), "task": e.Task,
			"exit": e.Exit, "ms": e.Duration.Milliseconds(), "session": e.Session, "log": e.Log, "content": capResult(e.Content),
		})
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
}

func (c *chat) trackSwarm(st core.SwarmStatus) {
	if len(st.Workers) == 0 {
		c.swarm = st
		c.delegate = st
		return
	}
	if delegateStatus(st) {
		c.delegate = st
	} else {
		c.swarm = st
	}
}

func delegateStatus(st core.SwarmStatus) bool {
	for _, w := range st.Workers {
		if w.Role == "delegate" {
			return true
		}
	}
	return false
}
