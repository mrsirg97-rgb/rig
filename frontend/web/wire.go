package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/models"
	"github.com/mrsirg97-rgb/rig/v3/room"
)

const (
	maxWireBody = 256 << 20
	gatePoll    = 50 * time.Millisecond
)

func mintOf(r *http.Request) (broadcast.Mint, bool) {
	v := r.Header.Get(core.MintHeader)
	if v == "" {
		return broadcast.Mint{}, false
	}
	var m broadcast.Mint
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		return broadcast.Mint{}, false
	}
	return m, true
}

func modelPath(path string) bool {
	return path == "/v1/models" || path == "/v1/chat/completions"
}

func wirePath(path string) bool {
	return modelPath(path) || path == "/running" || path == "/unload"
}

func (s *Server) handleWire(w http.ResponseWriter, r *http.Request, path string) {
	switch path {
	case "/v1/models":
		s.handleWireModels(w)
	case "/v1/chat/completions":
		s.handleWireChat(w, r)
	case "/running":
		s.handleRunning(w)
	case "/unload":
		s.handleUnload(w, r)
	}
}

func (s *Server) handleWireModels(w http.ResponseWriter) {
	data := []map[string]any{}
	for _, id := range s.models.Known() {
		m, _ := s.models.Get(id)
		row := map[string]any{"id": id, "object": "model"}
		if len(m.Aliases) > 0 {
			row["meta"] = map[string]any{"llamaswap": map[string]any{"aliases": m.Aliases}}
		}
		data = append(data, row)
		for _, alias := range m.Aliases {
			data = append(data, map[string]any{"id": alias, "object": "model"})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

func (s *Server) resolve(name string) (models.Model, bool) {
	if m, ok := s.models.Get(name); ok {
		return m, true
	}
	for _, id := range s.models.Known() {
		m, _ := s.models.Get(id)
		for _, alias := range m.Aliases {
			if alias == name {
				return m, true
			}
		}
	}
	return models.Model{}, false
}

func (s *Server) upstream(w http.ResponseWriter, r *http.Request, body []byte, row models.Model) {
	up, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.endpoint(row), bytes.NewReader(body))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	up.Header = r.Header.Clone()
	up.Header.Del(core.MintHeader)
	if up.Header.Get("Authorization") == "" && row.APIKey != "" {
		up.Header.Set("Authorization", "Bearer "+row.APIKey)
	}
	resp, err := http.DefaultClient.Do(up)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	for key, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			return
		}
	}
}

func (s *Server) handleWireChat(w http.ResponseWriter, r *http.Request) {
	if s.machine == nil {
		writeErr(w, http.StatusServiceUnavailable, "the wire front needs a member of the room")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWireBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the request body is over %d bytes", maxWireBody))
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "the request names no model: "+err.Error())
		return
	}
	row, ok := s.resolve(req.Model)
	if !ok {
		writeErr(w, http.StatusNotFound, "no such model: "+req.Model)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	key := room.KeyOf(row.ID)
	mint, minted := mintOf(r)
	gated := minted && s.door != nil
	var priority int
	if gated {
		serving := make(chan int, 1)
		gone := make(chan string, 1)
		refused := make(chan struct{}, 1)
		foreign := make(chan struct{}, 1)
		since := s.door.Horizon()
		go func() {
			tick := time.NewTicker(gatePoll)
			defer tick.Stop()
			for {
				holding, machine, priority := s.door.Holding(key, mint)
				if holding {
					serving <- priority
					return
				}
				if machine != "" {
					seen := make(map[string]bool)
					for _, m := range s.door.Machines() {
						seen[m] = true
					}
					if !seen[machine] {
						gone <- machine
						return
					}
				}
				if s.door.Foreign(mint) {
					foreign <- struct{}{}
					return
				}
				if s.door.Refused(key, mint, since) {
					refused <- struct{}{}
					return
				}
				select {
				case <-tick.C:
				case <-r.Context().Done():
					return
				}
			}
		}()
		select {
		case priority = <-serving:
		case <-r.Context().Done():
			return
		case m := <-gone:
			writeErr(w, http.StatusServiceUnavailable, "the machine holding "+m+" went silent: the mint's claim is refused")
			return
		case <-foreign:
			writeErr(w, http.StatusServiceUnavailable, "no door carries the key of "+req.Model+": the row's id must be the same on both machines")
			return
		case <-refused:
			writeErr(w, http.StatusServiceUnavailable, "the mint's claim never landed in the mesh's fold: the machine's clock may be wrong")
			return
		}
		defer s.door.Release(mint)
	} else {
		claimed := make(chan struct{})
		own := broadcast.Mint{}
		s.machine.Claim(key, room.PriorityOutside, 0, func(m broadcast.Mint, ok bool) {
			own = m
			close(claimed)
		})
		select {
		case <-claimed:
		case <-s.machine.Gone():
			writeErr(w, http.StatusServiceUnavailable, "this instance left the room for an update: restart it")
			return
		case <-r.Context().Done():
			go func() {
				select {
				case <-claimed:
					s.machine.Release(own, room.PriorityOutside)
				case <-s.machine.Gone():
				}
			}()
			return
		}
		defer s.machine.Release(own, room.PriorityOutside)
	}
	if s.ready != nil {
		if err := s.ready(r.Context(), row); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	s.upstream(w, r, body, row)
	if gated {
		_ = room.Record(s.home, row.ID, room.Served{At: time.Now().UTC(), Priority: priority})
	}
}

func (s *Server) endpoint(m models.Model) string {
	if m.Port > 0 {
		return "http://127.0.0.1:" + strconv.Itoa(m.Port) + "/v1/chat/completions"
	}
	return strings.TrimSuffix(m.BaseURL, "/") + "/chat/completions"
}

func (s *Server) handleRunning(w http.ResponseWriter) {
	running := []map[string]any{}
	for _, id := range s.models.Known() {
		m, _ := s.models.Get(id)
		if m.Port > 0 && room.Answers(m.Port) {
			running = append(running, map[string]any{"model": id})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"running": running})
}

func (s *Server) handleUnload(w http.ResponseWriter, r *http.Request) {
	if s.machine == nil {
		writeErr(w, http.StatusServiceUnavailable, "the wire front needs a member of the room")
		return
	}
	var req struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxWireBody)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	row, ok := s.resolve(req.Model)
	if !ok {
		writeErr(w, http.StatusNotFound, "no such model: "+req.Model)
		return
	}
	if row.Port <= 0 {
		writeErr(w, http.StatusBadRequest, "a remote row has nothing to unload: "+row.ID)
		return
	}
	pinned := make(chan bool, 1)
	s.machine.Act(room.StopChild{Model: row.ID, Grace: row.StopGrace}, room.KeyOf(row.ID), room.PriorityChild, func(ok bool) { pinned <- ok })
	select {
	case ok := <-pinned:
		if !ok {
			writeErr(w, http.StatusInternalServerError, "the stop was not admitted")
			return
		}
	case <-r.Context().Done():
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unloaded": row.ID})
}
