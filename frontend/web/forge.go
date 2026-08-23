package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mrsirg97-rgb/rig/plugins"
)

func (s *Server) pluginPath(name, zone string) (string, bool) {
	switch zone {
	case "loaded":
		return filepath.Join(s.home, "plugins", name+".py"), true
	case "pending":
		return filepath.Join(s.home, "plugins", "pending", name+".py"), true
	case "disabled":
		return filepath.Join(s.home, "plugins", "disabled", name+".py"), true
	}
	return "", false
}

func pluginNameOK(w http.ResponseWriter, name string) bool {
	if pluginNameRe.MatchString(name) {
		return true
	}
	writeErr(w, http.StatusBadRequest,
		"the name is the filename stem: lowercase, digits and underscores, a leading letter (got "+strconv.Quote(name)+")")
	return false
}

func (s *Server) handlePluginSource(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	zone := r.URL.Query().Get("zone")
	if !pluginNameOK(w, name) {
		return
	}
	path, ok := s.pluginPath(name, zone)
	if !ok {
		writeErr(w, http.StatusBadRequest, "zone is loaded, pending, or disabled (got "+strconv.Quote(zone)+")")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "no "+zone+" plugin '"+name+"'")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "zone": zone, "file": path, "source": string(data)})
}

func (s *Server) writeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	if !s.originOK(r) {
		writeErr(w, http.StatusForbidden, "origin mismatch (same-origin only)")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWriteBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return false
	}
	if err := json.Unmarshal(body, into); err != nil {
		writeErr(w, http.StatusBadRequest, "a JSON body is required: "+err.Error())
		return false
	}
	return true
}

func (s *Server) handlePluginSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	if !s.writeBody(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	path, created, err := plugins.WritePending(s.home, s.natives, name, in.Source)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	verb := "updated"
	if created {
		verb = "created"
	}
	reply := verb + " '" + name + "' in plugins/pending/" + name + ".py (approve it to load)"
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "file": path, "created": created, "reply": reply})
}

func (s *Server) handlePluginApprove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string `json:"name"`
		Replace bool   `json:"replace"`
	}
	if !s.writeBody(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	if !pluginNameOK(w, name) {
		return
	}
	src, _ := s.pluginPath(name, "pending")
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "no pending plugin '"+name+"'")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.natives[name] {
		writeErr(w, http.StatusBadRequest, "name collision: '"+name+"' is a native tool")
		return
	}
	dst, _ := s.pluginPath(name, "loaded")
	if _, err := os.Stat(dst); err == nil && !in.Replace {
		writeErr(w, http.StatusConflict, "'"+name+"' is already installed (plugins/"+name+".py); approve with replace to overwrite it")
		return
	}
	if err := os.Rename(src, dst); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	verb := "approved"
	if in.Replace {
		verb = "approved and replaced"
	}
	reply := verb + " '" + name + "' (pending -> plugins); a live session loads it at its next plugins reload"
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "file": dst, "reply": reply})
}

func (s *Server) handlePluginDisable(w http.ResponseWriter, r *http.Request) {
	s.handlePluginMove(w, r, "disable", "", "disabled")
}

func (s *Server) handlePluginEnable(w http.ResponseWriter, r *http.Request) {
	s.handlePluginMove(w, r, "enable", "disabled", "")
}

func (s *Server) handlePluginMove(w http.ResponseWriter, r *http.Request, verb, from, to string) {
	var in struct {
		Name string `json:"name"`
	}
	if !s.writeBody(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	if !pluginNameOK(w, name) {
		return
	}
	zone := func(z string) string {
		if z == "" {
			return "plugins"
		}
		return "plugins/" + z
	}
	if _, _, err := plugins.Move(filepath.Join(s.home, "plugins"), name, from, to); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			writeErr(w, http.StatusConflict, "'"+name+"' is already in the "+zone(to)+" zone")
			return
		}
		writeErr(w, http.StatusNotFound, "no plugin '"+name+"' in "+zone(from))
		return
	}
	var reply string
	if verb == "disable" {
		reply = "disabled '" + name + "' (plugins -> plugins/disabled); hidden next turn"
	} else {
		reply = "enabled '" + name + "' (plugins/disabled -> plugins); live at the next plugins reload"
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "reply": reply})
}
