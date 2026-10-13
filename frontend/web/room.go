package web

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/v3/room"
)

const (
	logTail      = 200
	logTailBytes = 64 * 1024
	servedShown  = 12
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
				"tail":    tail(filepath.Join(s.home, "logs", id+".log"), logTail),
				"served":  room.Recent(s.home, id, servedShown),
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
	lines := []string{}
	f, err := os.Open(path)
	if err != nil {
		return lines
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return lines
	}
	from := info.Size() - logTailBytes
	if from < 0 {
		from = 0
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return lines
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return lines
	}
	all := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if from > 0 && len(all) > 0 {
		all = all[1:]
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	for _, l := range all {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}
