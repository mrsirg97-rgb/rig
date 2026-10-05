package web

import (
	"encoding/json"
	"fmt"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleTodoCreate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.readCtx(r)
	defer cancel()
	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		writeErr(w, http.StatusBadRequest, "cwd is required")
		return
	}
	if !s.originOK(r) {
		writeErr(w, http.StatusForbidden, "origin mismatch (same-origin only)")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWriteBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	lines := splitTasks(string(body))
	if len(lines) == 0 {
		writeErr(w, http.StatusBadRequest, "no tasks (one per line)")
		return
	}
	db, err := s.stores.todo(cwd)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var reply string
	for _, l := range lines {
		reply, err = todostore.Create(ctx, db, todoProject(cwd), todostore.CreateItem{Text: l}, sessionName)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"cwd": cwd, "reply": reply})
}

func (s *Server) handleSchedulerCreate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.readCtx(r)
	defer cancel()
	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		writeErr(w, http.StatusBadRequest, "cwd is required")
		return
	}
	if !s.originOK(r) {
		writeErr(w, http.StatusForbidden, "origin mismatch (same-origin only)")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWriteBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var in sched.CreateInput
	if err := json.Unmarshal(body, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "a JSON body {name, prompt, cron, at?, cwd?} is required: "+err.Error())
		return
	}
	sdb, err := s.stores.scheduler()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	reply, err := sched.Create(ctx, sdb, s.crontab,
		in, cwd, sessionName, s.runnerCmd, s.home, time.Now)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cwd": cwd, "reply": reply})
}

func (s *Server) handlePluginsCreate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.readCtx(r)
	defer cancel()
	if !s.originOK(r) {
		writeErr(w, http.StatusForbidden, "origin mismatch (same-origin only)")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWriteBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Code        string `json:"code"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "a JSON body {name, description, code} is required: "+err.Error())
		return
	}
	name := strings.TrimSpace(in.Name)
	if !pluginNameRe.MatchString(name) {
		writeErr(w, http.StatusBadRequest,
			"the name is the filename stem: lowercase, digits and underscores, a leading letter (got "+strconv.Quote(name)+")")
		return
	}
	code := strings.TrimRight(in.Code, " \t\r\n")
	if code == "" {
		writeErr(w, http.StatusBadRequest, "the run body is required (a run with no body is no plugin)")
		return
	}
	loaded, pending, disabled, err := listPlugins(s.home)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, p := range disabled {
		if p.Name == name {
			writeErr(w, http.StatusBadRequest, "a plugin named '"+name+"' already exists (disabled); enable or remove it first")
			return
		}
	}
	for _, p := range loaded {
		if p.Name == name {
			writeErr(w, http.StatusBadRequest, "a plugin named '"+name+"' already exists (loaded); remove it first")
			return
		}
	}
	for _, p := range pending {
		if p.Name == name {
			writeErr(w, http.StatusBadRequest, "a plugin named '"+name+"' already exists (pending); remove it first")
			return
		}
	}
	dir := filepath.Join(s.home, "plugins", "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	path := filepath.Join(dir, name+".py")
	if _, err := os.Stat(path); err == nil {
		writeErr(w, http.StatusBadRequest, "a plugin named '"+name+"' already exists (pending); remove it first")
		return
	}
	desc, err := json.Marshal(in.Description)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "DESCRIPTION = %s\n", desc)
	b.WriteString("SCHEMA = {\"type\": \"object\"}\n\n")
	b.WriteString("def run(args):\n")
	for _, line := range strings.Split(code, "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("    " + strings.TrimLeft(line, " \t") + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rel, err := filepath.Rel(s.home, path)
	if err != nil {
		rel = path
	}
	reply := "created '" + name + "' in " + filepath.ToSlash(rel) + " (the pending zone: move it into plugins/ and reload to load)"
	_ = ctx
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "file": path, "reply": reply})
}
