package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3/command"
	"github.com/mrsirg97-rgb/rig/v3/core"
)

const (
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

func (s *Server) Input(ctx context.Context) (string, error) { return s.chat.Input(ctx) }
func (s *Server) Returns() (string, bool)                   { return s.chat.Returns() }
func (s *Server) Notify(ev evt.Signal)                      { s.chat.Notify(ev) }
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

func (s *Server) handleSwarm(w http.ResponseWriter, r *http.Request) {
	s.chat.hub.Lock()
	st := s.chat.swarm
	del := s.chat.delegate
	s.chat.hub.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"workers": swarmRows(st.Workers), "pending": st.Pending, "review": st.Review,
		"delegate": swarmRows(del.Workers),
	})
}

func swarmRows(ws []core.SwarmWorker) []map[string]any {
	out := make([]map[string]any, 0, len(ws))
	for _, w := range ws {
		out = append(out, map[string]any{
			"id": w.ID, "role": w.Role, "task": w.Task, "state": w.State,
			"done": w.Done, "failed": w.Failed, "heartbeat": w.Heartbeat.UTC().Format(time.RFC3339),
			"tool": w.Tool, "tool_at": w.ToolAt.UTC().Format(time.RFC3339),
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
