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
	if !pluginNameOK(w, name) {
		return
	}
	if s.natives[name] {
		writeErr(w, http.StatusBadRequest, "name collision: '"+name+"' is a native tool")
		return
	}
	source := strings.TrimRight(in.Source, " \t\r\n") + "\n"
	if strings.TrimSpace(source) == "" {
		writeErr(w, http.StatusBadRequest, "the source is required")
		return
	}
	for _, want := range []string{"DESCRIPTION", "SCHEMA", "def run("} {
		if !strings.Contains(source, want) {
			writeErr(w, http.StatusBadRequest, "the plugin contract is a DESCRIPTION, a SCHEMA, and a run(args): missing "+want)
			return
		}
	}
	dir := filepath.Join(s.home, "plugins", "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	path := filepath.Join(dir, name+".py")
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
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
