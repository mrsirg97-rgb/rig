package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/models"
	"github.com/mrsirg97-rgb/rig/v3/room"
)

const (
	childProbePause = 100 * time.Millisecond
	childStopPoll   = 10 * time.Millisecond
)

type children struct {
	home   string
	owned  map[string]int
	rows   models.Table
	loop   evt.Engine
	leave  func()
	closed bool
	engine evt.Engine
	cancel context.CancelFunc
	self   int64
}

func newChildren(home string, rows models.Table, loop evt.Engine, leave func()) *children {
	engine := evt.NewEngine()
	ctx, cancel := context.WithCancel(context.Background())
	go engine.Start(ctx)
	return &children{home: home, owned: make(map[string]int), rows: rows, loop: loop, leave: leave, engine: engine, cancel: cancel}
}

func (c *children) close() { c.cancel() }

func (c *children) effects(self int64) broadcast.Effects {
	c.self = self
	return c
}

func (c *children) places(key int64) int {
	for _, id := range c.rows.Known() {
		if room.KeyOf(id) == key {
			m, _ := c.rows.Get(id)
			return m.Places
		}
	}
	return 0
}

func (c *children) Next(op broadcast.Operation) error {
	switch act := op.Signal().(type) {
	case room.StartChild:
		if op.Origin() != c.self {
			return nil
		}
		c.engine.Add(evt.Func(func(context.Context) {
			if !c.starting(act.Model, act.Port, act.Command, act.Env, act.Log) {
				c.note(act.Model, fmt.Sprintf("the child's port %d never answered", act.Port))
			}
		}), room.PriorityChild)
	case room.StopChild:
		if op.Origin() != c.self {
			return nil
		}
		c.engine.Add(evt.Func(func(context.Context) { c.stopping(act.Model, act.Grace) }), room.PriorityChild)
	case room.SwapChild:
		if op.Origin() != c.self {
			return nil
		}
		c.engine.Add(evt.Func(func(context.Context) {
			c.stopping(act.Out, act.Grace)
			if act.Model == "" {
				return
			}
			if !c.starting(act.Model, act.Port, act.Command, act.Env, act.Log) {
				c.note(act.Model, fmt.Sprintf("the child's port %d never answered", act.Port))
			}
		}), room.PriorityChild)
	case room.TableChange:
		var row models.Model
		if err := json.Unmarshal(act.Row, &row); err != nil {
			return err
		}
		if known, ok := c.rows.Get(row.ID); ok {
			row.Window = known.Window
			row.MaxTokens = known.MaxTokens
			row.Reserve = known.Reserve
			row.KeepRecent = known.KeepRecent
		}
		var rows []models.Model
		for _, id := range c.rows.Known() {
			m, _ := c.rows.Get(id)
			if id == row.ID {
				m = row
			}
			rows = append(rows, m)
		}
		if _, ok := c.rows.Get(row.ID); !ok {
			rows = append(rows, row)
		}
		merged, err := models.New(rows...)
		if err != nil {
			return err
		}
		c.rows = merged
	case room.Update:
		if c.closed || c.leave == nil {
			return nil
		}
		c.closed = true
		if c.loop == nil {
			c.leave()
			return nil
		}
		c.loop.Add(evt.Func(func(context.Context) { c.leave() }), room.PriorityTurn)
	}
	return nil
}

func (c *children) Previous(op broadcast.Operation) error { return nil }

func (c *children) resident(machine room.Member) []string {
	type child struct {
		id   string
		port int
	}
	live := []child{}
	if !machine.Ask(func() {
		for _, id := range c.rows.Known() {
			m, _ := c.rows.Get(id)
			if m.Port > 0 {
				live = append(live, child{id, m.Port})
			}
		}
	}) {
		return nil
	}
	out := []string{}
	for _, ch := range live {
		if room.Answers(ch.port) {
			out = append(out, ch.id)
		}
	}
	return out
}

var errLeft = errors.New("rig: this instance left the room for an update: restart it")

func (c *children) ready(ctx context.Context, machine room.Member, row models.Model) error {
	if row.Port <= 0 {
		return nil
	}
	if !room.Answers(row.Port) {
		var out []models.Model
		for _, id := range c.resident(machine) {
			if id == row.ID {
				continue
			}
			var other models.Model
			if machine.Ask(func() { other, _ = c.rows.Get(id) }) {
				out = append(out, other)
			}
		}
		in := room.SwapChild{Model: row.ID, Port: row.Port, Command: row.Command, Env: row.Env}
		for i, other := range out {
			act := room.SwapChild{Out: other.ID, Grace: other.StopGrace}
			if i == len(out)-1 {
				act = in
				act.Out, act.Grace = other.ID, other.StopGrace
			}
			if err := c.act(ctx, machine, act, room.KeyOf(other.ID)); err != nil {
				return err
			}
		}
		if len(out) == 0 {
			start := room.StartChild{Model: row.ID, Port: row.Port, Command: row.Command, Env: row.Env}
			if err := c.act(ctx, machine, start, room.KeyOf(row.ID)); err != nil {
				return err
			}
		}
		if !room.Answers(row.Port) {
			return fmt.Errorf("rig: %s: the child did not start (its output is %s)", row.ID, filepath.Join(c.home, "logs", row.ID+".log"))
		}
	}
	for room.Loading(row.Port) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(childProbePause):
		}
	}
	return nil
}

func (c *children) act(ctx context.Context, machine room.Member, act evt.Signal, key int64) error {
	pinned := make(chan struct{})
	machine.Act(act, key, room.PriorityChild, func(bool) { close(pinned) })
	select {
	case <-pinned:
	case <-ctx.Done():
		return ctx.Err()
	case <-machine.Gone():
		return errLeft
	}
	done := make(chan struct{})
	c.engine.Add(evt.Func(func(context.Context) { close(done) }), room.PriorityChild)
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *children) start(model string, port int, command string, env []string, log string) bool {
	done := make(chan bool, 1)
	c.engine.Add(evt.Func(func(context.Context) { done <- c.starting(model, port, command, env, log) }), room.PriorityChild)
	return <-done
}

func (c *children) starting(model string, port int, command string, env []string, log string) bool {
	if port <= 0 || command == "" {
		return false
	}
	if room.Answers(port) {
		return true
	}
	dir := filepath.Join(c.home, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	if log == "" {
		log = filepath.Join(dir, model+".log")
	}
	pid, mine := c.held(model), false
	if pid == 0 {
		f, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return false
		}
		defer f.Close()
		cmd := exec.Command("sh", "-c", strings.ReplaceAll(command, "${PORT}", strconv.Itoa(port)))
		cmd.Stdout = f
		cmd.Stderr = f
		cmd.Env = append(os.Environ(), env...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(f, "rig: %s: start: %v\n", model, err)
			return false
		}
		pid, mine = cmd.Process.Pid, true
		c.owned[model] = pid
		if !c.hold(model, pid) {
			return false
		}
		go func() {
			cmd.Wait()
			c.engine.Add(evt.Func(func(context.Context) { c.childGone(model, pid) }), room.PriorityChild)
		}()
	}
	for alive(pid) {
		if !room.Answers(port) {
			time.Sleep(childProbePause)
			continue
		}
		if !mine {
			return true
		}
		for other := c.held(model); other != 0 && other != pid && alive(pid); other = c.held(model) {
			time.Sleep(childProbePause)
		}
		if alive(pid) && !c.hold(model, pid) {
			return false
		}
		return true
	}
	if mine {
		c.owned[model] = 0
	}
	return room.Answers(port)
}

func (c *children) childGone(model string, pid int) {
	if c.owned[model] == pid {
		c.owned[model] = 0
	}
}

func (c *children) hold(model string, pid int) bool {
	line := strconv.Itoa(pid) + " " + strconv.FormatInt(startOf(pid), 10)
	return os.WriteFile(filepath.Join(c.home, "logs", model+".pid"), []byte(line), 0o644) == nil
}

func (c *children) named(model string) (int, int64) {
	b, err := os.ReadFile(filepath.Join(c.home, "logs", model+".pid"))
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(string(b))
	if len(fields) != 2 {
		return 0, 0
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 0 {
		return 0, 0
	}
	start, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, 0
	}
	return pid, start
}

func (c *children) held(model string) int {
	pid, start := c.named(model)
	if pid == 0 || !alive(pid) || startOf(pid) != start {
		return 0
	}
	return pid
}

func startOf(pid int) int64 {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	end := strings.LastIndex(string(stat), ") ")
	if end < 0 {
		return 0
	}
	fields := strings.Fields(string(stat[end+2:]))
	if len(fields) < 20 {
		return 0
	}
	start, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0
	}
	return start
}

func (c *children) note(model, text string) {
	dir := filepath.Join(c.home, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, model+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "rig: %s: %s\n", model, text)
}

func (c *children) stop(model string, grace int) {
	done := make(chan struct{})
	c.engine.Add(evt.Func(func(context.Context) { c.stopping(model, grace); close(done) }), room.PriorityChild)
	<-done
}

func (c *children) stopping(model string, grace int) {
	pid := c.pidOf(model)
	if pid <= 0 {
		return
	}
	sys, _ := os.FindProcess(pid)
	if sys == nil {
		return
	}
	syscall.Kill(-pid, syscall.SIGTERM)
	deadline := time.Duration(grace) * time.Second
	if !c.waitGone(pid, deadline) {
		syscall.Kill(-pid, syscall.SIGKILL)
		c.waitGone(pid, deadline)
	}
	c.owned[model] = 0
}

func (c *children) pidOf(model string) int {
	if pid := c.owned[model]; pid > 0 && alive(pid) {
		return pid
	}
	return c.held(model)
}

func (c *children) waitGone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(childStopPoll)
	}
	return !alive(pid)
}

func alive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
