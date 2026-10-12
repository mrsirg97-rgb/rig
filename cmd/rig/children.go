package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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
	childProbeTries = 300
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
}

func newChildren(home string, rows models.Table, loop evt.Engine, leave func()) *children {
	return &children{home: home, owned: make(map[string]int), rows: rows, loop: loop, leave: leave}
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
		if !c.start(act.Model, act.Port, act.Command, act.Env, act.Log) {
			return fmt.Errorf("rig: %s: the child's port %d never answered", act.Model, act.Port)
		}
	case room.StopChild:
		c.stop(act.Model, act.Grace)
	case room.SwapChild:
		c.stop(act.Out, act.Grace)
		if !c.start(act.Model, act.Port, act.Command, act.Env, act.Log) {
			return fmt.Errorf("rig: %s: the child's port %d never answered", act.Model, act.Port)
		}
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
		c.loop.Add(evt.Func(func(context.Context) { c.leave() }), room.PriorityTurn)
	}
	return nil
}

func (c *children) Previous(op broadcast.Operation) error { return nil }

func (c *children) start(model string, port int, command string, env []string, log string) bool {
	if port > 0 && probe(port) {
		return true
	}
	dir := filepath.Join(c.home, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	if log == "" {
		log = filepath.Join(dir, model+".log")
	}
	f, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return false
	}
	defer f.Close()
	pidPath := filepath.Join(dir, model+".pid")
	if pid := c.pidOf(model); pid > 0 {
		if sys, err := os.FindProcess(pid); err == nil {
			sys.Kill()
			c.waitGone(pid, 5*time.Second)
		}
	}
	cmd := exec.Command("sh", "-c", strings.ReplaceAll(command, "${PORT}", strconv.Itoa(port)))
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(f, "rig: %s: start: %v\n", model, err)
		return false
	}
	c.owned[model] = cmd.Process.Pid
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		return false
	}
	for i := 0; i < childProbeTries; i++ {
		if probe(port) {
			return true
		}
		time.Sleep(childProbePause)
	}
	return false
}

func (c *children) stop(model string, grace int) {
	pid := c.pidOf(model)
	if pid <= 0 {
		return
	}
	sys, _ := os.FindProcess(pid)
	if sys == nil {
		return
	}
	sys.Signal(syscall.SIGTERM)
	deadline := time.Duration(grace) * time.Second
	if !c.waitGone(pid, deadline) {
		sys.Kill()
		c.waitGone(pid, deadline)
	}
	c.owned[model] = 0
	if err := os.Remove(filepath.Join(c.home, "logs", model+".pid")); err != nil && !os.IsNotExist(err) {
		return
	}
}

func (c *children) pidOf(model string) int {
	if pid := c.owned[model]; pid > 0 {
		return pid
	}
	b, err := os.ReadFile(filepath.Join(c.home, "logs", model+".pid"))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
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

func probe(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
