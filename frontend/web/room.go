package web

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mrsirg97-rgb/rig/v3/room"
)

func (s *Server) handleRoom(w http.ResponseWriter, r *http.Request) {
	members := []int64{}
	models := map[string]any{}
	if s.machine != nil {
		live := s.machine.Live()
		members = append(members, live...)
		for _, id := range s.models.Known() {
			m, ok := s.models.Get(id)
			if !ok {
				continue
			}
			key := room.KeyOf(id)
			models[id] = map[string]any{
				"holders": len(s.machine.Holders(key)),
				"waiting": len(s.machine.Waiting(key)),
				"live":    m.Port == 0 || room.Answers(m.Port),
				"tail":    tail(filepath.Join(s.home, "logs", id+".log"), 8),
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members, "models": models})
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		self, err := os.Executable()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		cmd := exec.Command(self, "-update")
		cmd.Env = append(os.Environ(), "HOME="+s.root)
		out, err := cmd.CombinedOutput()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "out": string(out)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"out": string(out)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": s.version})
}

func tail(path string, n int) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return []string{}
	}
	var lines []string
	start := len(b)
	for i := len(b) - 1; i >= 0 && len(lines) < n; i-- {
		if b[i] == '\n' {
			if start > i+1 {
				lines = append(lines, string(b[i+1:start]))
			}
			start = i
		}
	}
	if len(lines) < n && start < len(b) {
		lines = append(lines, string(b[:start]))
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return lines
}
