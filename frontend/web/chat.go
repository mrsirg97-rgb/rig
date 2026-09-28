package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/command"
	"github.com/mrsirg97-rgb/rig/v2/core"
)

const (
	ringCap        = 4096
	coalesceBytes  = 4096
	resultCapBytes = 16 * 1024
	heartbeat      = 15 * time.Second
)

type Status struct {
	Model     string   `json:"model"`
	Effort    string   `json:"effort"`
	Window    int      `json:"window"`
	Role      string   `json:"role"`
	Approve   string   `json:"approve"`
	Workers   string   `json:"workers"`
	Session   string   `json:"session"`
	Up        int      `json:"up"`
	Down      int      `json:"down"`
	CacheRead int      `json:"cache_read"`
	Cost      float64  `json:"cost"`
	Rows      []string `json:"rows"`
}

type chat struct {
	lines chan string
	slot  chan string

	mu          sync.Mutex
	reading     bool
	cancel      context.CancelFunc
	turnCtx     context.Context
	steeredLive bool

	commands map[string]core.Command
	known    []string
	env      any

	hub  sync.Mutex
	subs map[chan []byte]struct{}
	ring [][]byte
	seq  int64
	last map[string]any

	asks   map[string]chan bool
	askSeq int64

	swarm core.SwarmStatus
}

func newChat(cmds []core.Command, env any) *chat {
	c := &chat{
		lines: make(chan string, 1),
		slot:  make(chan string, 1),
		subs:  map[chan []byte]struct{}{},
		asks:  map[string]chan bool{},
		env:   env,
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

func (c *chat) publish(f map[string]any) {
	c.hub.Lock()
	defer c.hub.Unlock()
	kind, _ := f["kind"].(string)
	if (kind == "text" || kind == "reasoning") && c.last != nil && c.last["kind"] == kind {
		prev, _ := c.last["text"].(string)
		next, _ := f["text"].(string)
		if len(prev)+len(next) <= coalesceBytes {
			c.last["text"] = prev + next
			c.ring[len(c.ring)-1] = mustJSON(c.last)
			return
		}
	}
	c.seq++
	f["seq"] = c.seq
	c.last = f
	raw := mustJSON(f)
	c.ring = append(c.ring, raw)
	if len(c.ring) > ringCap {
		c.ring = c.ring[len(c.ring)-ringCap:]
	}
	for ch := range c.subs {
		select {
		case ch <- raw:
		default:
		}
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"kind":"fault","text":"frame did not marshal"}`)
	}
	return b
}

func (c *chat) subscribe(since int64) (chan []byte, [][]byte, int64) {
	c.hub.Lock()
	defer c.hub.Unlock()
	ch := make(chan []byte, 256)
	c.subs[ch] = struct{}{}
	var replay [][]byte
	for _, raw := range c.ring {
		var head struct {
			Seq int64 `json:"seq"`
		}
		if json.Unmarshal(raw, &head) == nil && head.Seq > since {
			replay = append(replay, raw)
		}
	}
	return ch, replay, c.seq
}

func (c *chat) unsubscribe(ch chan []byte) {
	c.hub.Lock()
	delete(c.subs, ch)
	c.hub.Unlock()
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
	c.reading = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.reading = false
		c.mu.Unlock()
	}()
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		select {
		case line := <-c.slot:
			if strings.TrimSpace(line) == "" {
				continue
			}
			return c.take(ctx, line), nil
		default:
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case line := <-c.lines:
			if strings.TrimSpace(line) == "" {
				continue
			}
			out := line
			if strings.HasPrefix(line, "//") {
				out = command.Unescape(line)
			}
			return c.take(ctx, out), nil
		}
	}
}

func (c *chat) take(ctx context.Context, line string) string {
	c.mu.Lock()
	c.turnCtx = ctx
	c.steeredLive = false
	c.mu.Unlock()
	c.publish(map[string]any{"kind": "prompt", "text": line})
	return line
}

func (c *chat) dispatch(ctx context.Context, line string) map[string]any {
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
	c.mu.Unlock()
	if live && cancel != nil {
		cancel()
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

func usageOf(u core.Usage) map[string]any {
	return map[string]any{"prompt": u.Prompt, "completion": u.Completion, "cache_read": u.CacheRead, "cache_write": u.CacheWrite, "cost": u.Cost}
}

func (c *chat) Notify(ev core.Event) {
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
		c.swarm = e
		c.hub.Unlock()
		c.publish(map[string]any{"kind": "swarm_status", "workers": swarmRows(e.Workers), "pending": e.Pending, "review": e.Review})
	case core.SwarmNotice:
		c.publish(map[string]any{"kind": "swarm_notice", "text": e.Text})
	}
}

func swarmRows(ws []core.SwarmWorker) []map[string]any {
	out := make([]map[string]any, 0, len(ws))
	for _, w := range ws {
		out = append(out, map[string]any{
			"id": w.ID, "role": w.Role, "task": w.Task, "state": w.State,
			"done": w.Done, "failed": w.Failed, "heartbeat": w.Heartbeat.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func capResult(s string) string {
	if len(s) <= resultCapBytes {
		return s
	}
	return s[:resultCapBytes] + fmt.Sprintf("\n· %d more bytes hidden ·", len(s)-resultCapBytes)
}

func (s *Server) Input(ctx context.Context) (string, error) { return s.chat.Input(ctx) }
func (s *Server) Notify(ev core.Event)                      { s.chat.Notify(ev) }
func (s *Server) Ask(ctx context.Context, prompt string) bool {
	return s.chat.Ask(ctx, prompt)
}
func (s *Server) Steer(text string) bool { return s.chat.Steer(text) }
func (s *Server) Interrupt() bool        { return s.chat.Interrupt() }
func (s *Server) ClearSlot()             { s.chat.ClearSlot() }
func (s *Server) LiveTurn() bool         { return s.chat.LiveTurn() }

func (s *Server) handleChatEvents(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, replay, seq := s.chat.subscribe(since)
	defer s.chat.unsubscribe(ch)
	write := func(raw []byte) bool {
		if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !write(mustJSON(map[string]any{"kind": "hello", "seq": seq, "live": s.chat.LiveTurn(), "replay": len(replay)})) {
		return
	}
	for _, raw := range replay {
		if !write(raw) {
			return
		}
	}
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case raw := <-ch:
			if !write(raw) {
				return
			}
		case <-tick.C:
			if _, err := io.WriteString(w, ": keep\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request) {
	if !s.originOK(r) {
		writeErr(w, http.StatusForbidden, "origin refused")
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxWriteBytes)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "body: "+err.Error())
		return
	}
	text := strings.TrimRight(in.Text, "\r\n")
	if strings.TrimSpace(text) == "" {
		writeErr(w, http.StatusBadRequest, "text is empty")
		return
	}
	if s.chat.commands != nil && command.IsCommandLine(text) {
		f := s.chat.dispatch(r.Context(), text)
		writeJSON(w, http.StatusOK, f)
		return
	}
	if s.chat.LiveTurn() {
		s.chat.publish(map[string]any{"kind": "steer", "text": text})
		s.chat.Steer(text)
		writeJSON(w, http.StatusOK, map[string]any{"kind": "steer", "text": text})
		return
	}
	select {
	case s.chat.lines <- text:
		writeJSON(w, http.StatusOK, map[string]any{"kind": "queued", "text": text})
	default:
		writeErr(w, http.StatusConflict, "a prompt is already queued; wait for the turn to start")
	}
}

func (s *Server) handleChatAnswer(w http.ResponseWriter, r *http.Request) {
	if !s.originOK(r) {
		writeErr(w, http.StatusForbidden, "origin refused")
		return
	}
	var in struct {
		ID  string `json:"id"`
		Yes bool   `json:"yes"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxWriteBytes)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "body: "+err.Error())
		return
	}
	if !s.chat.answer(in.ID, in.Yes) {
		writeErr(w, http.StatusNotFound, "no open question "+in.ID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": in.ID, "yes": in.Yes})
}

func (s *Server) handleChatInterrupt(w http.ResponseWriter, r *http.Request) {
	if !s.originOK(r) {
		writeErr(w, http.StatusForbidden, "origin refused")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"interrupted": s.chat.Interrupt()})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	var st Status
	if s.status != nil {
		ctx, cancel := s.readCtx(r)
		defer cancel()
		st = s.status(ctx)
	}
	if st.Rows == nil {
		st.Rows = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": st, "live": s.chat.LiveTurn(), "seq": s.chat.seqNow()})
}

func (c *chat) seqNow() int64 {
	c.hub.Lock()
	defer c.hub.Unlock()
	return c.seq
}

func (s *Server) handleSwarm(w http.ResponseWriter, r *http.Request) {
	s.chat.hub.Lock()
	st := s.chat.swarm
	s.chat.hub.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"workers": swarmRows(st.Workers), "pending": st.Pending, "review": st.Review})
}
