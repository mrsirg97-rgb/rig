package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	"github.com/mrsirg97-rgb/rig/store/scope"
	"github.com/mrsirg97-rgb/rig/store/state"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

const (
	maxWriteBytes = 64 * 1024
	defaultReadTO = 5 * time.Second
	defaultPage   = 200
	maxPage       = 1000
)

var pluginNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func (s *Server) router() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		allowed, ok := s.allowed(path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if !allowed[r.Method] {
			w.Header().Set("Allow", strings.Join(methods(allowed), ", "))
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.dispatch(w, r, path)
	})
}

func (s *Server) allowed(path string) (map[string]bool, bool) {
	switch {
	case path == "/":
		return setOf("GET"), true
	case strings.HasPrefix(path, "/static/"):
		return setOf("GET"), true
	case path == "/api/cwds":
		return setOf("GET"), true
	case path == "/api/sessions":
		return setOf("GET"), true
	case isTranscriptPath(path):
		return setOf("GET"), true
	case path == "/api/todo":
		return setOf("GET", "POST"), true
	case path == "/api/todo/start" || path == "/api/todo/complete" || path == "/api/todo/retry":
		return setOf("POST"), true
	case path == "/api/scheduler":
		return setOf("GET", "POST"), true
	case path == "/api/scheduler/pause" || path == "/api/scheduler/resume" ||
		path == "/api/scheduler/remove" || path == "/api/scheduler/update":
		return setOf("POST"), true
	case path == "/api/scheduler/runs":
		return setOf("GET"), true
	case path == "/api/models":
		return setOf("GET"), true
	case path == "/api/plugins":
		return setOf("GET", "POST"), true
	case path == "/api/plugins/source":
		return setOf("GET"), true
	case path == "/api/plugins/save":
		return setOf("POST"), true
	case path == "/api/plugins/approve":
		return setOf("POST"), true
	case path == "/api/plugins/disable" || path == "/api/plugins/enable":
		return setOf("POST"), true
	case path == "/api/fs":
		return setOf("GET"), true
	}
	return nil, false
}

func (s *Server) dispatch(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case path == "/":
		s.serveStatic(w, "index.html", "text/html; charset=utf-8")
	case strings.HasPrefix(path, "/static/"):
		s.serveStaticFile(w, r, strings.TrimPrefix(path, "/static/"))
	case path == "/api/cwds":
		s.handleCwds(w, r)
	case path == "/api/sessions":
		s.handleSessions(w, r)
	case isTranscriptPath(path):
		s.handleTranscript(w, r, transcriptID(path))
	case path == "/api/todo" && r.Method == "GET":
		s.handleTodoRead(w, r)
	case path == "/api/todo" && r.Method == "POST":
		s.handleTodoCreate(w, r)
	case path == "/api/todo/start":
		s.handleTodoVerb(w, r, "start")
	case path == "/api/todo/complete":
		s.handleTodoVerb(w, r, "complete")
	case path == "/api/todo/retry":
		s.handleTodoVerb(w, r, "retry")
	case path == "/api/scheduler" && r.Method == "GET":
		s.handleScheduler(w, r)
	case path == "/api/scheduler" && r.Method == "POST":
		s.handleSchedulerCreate(w, r)
	case path == "/api/scheduler/pause":
		s.handleSchedulerVerb(w, r, "pause")
	case path == "/api/scheduler/resume":
		s.handleSchedulerVerb(w, r, "resume")
	case path == "/api/scheduler/remove":
		s.handleSchedulerVerb(w, r, "remove")
	case path == "/api/scheduler/update":
		s.handleSchedulerUpdate(w, r)
	case path == "/api/scheduler/runs":
		s.handleSchedulerRuns(w, r)
	case path == "/api/models":
		s.handleModels(w, r)
	case path == "/api/plugins" && r.Method == "GET":
		s.handlePlugins(w, r)
	case path == "/api/plugins" && r.Method == "POST":
		s.handlePluginsCreate(w, r)
	case path == "/api/plugins/source":
		s.handlePluginSource(w, r)
	case path == "/api/plugins/save":
		s.handlePluginSave(w, r)
	case path == "/api/plugins/approve":
		s.handlePluginApprove(w, r)
	case path == "/api/plugins/disable":
		s.handlePluginDisable(w, r)
	case path == "/api/plugins/enable":
		s.handlePluginEnable(w, r)
	case path == "/api/fs":
		s.handleBrowse(w, r)
	}
}

func isTranscriptPath(path string) bool {
	const prefix = "/api/sessions/"
	const suffix = "/transcript"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return false
	}
	id := path[len(prefix) : len(path)-len(suffix)]
	return id != "" && !strings.Contains(id, "/")
}

func transcriptID(path string) string {
	const prefix = "/api/sessions/"
	const suffix = "/transcript"
	id := path[len(prefix) : len(path)-len(suffix)]
	return id
}

func (s *Server) handleCwds(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.readCtx(r)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{"cwds": s.workspaces(ctx)})
}

func (s *Server) workspaces(ctx context.Context) []string {
	seen := map[string]bool{}
	var others []string
	add := func(cwd string) {
		if cwd == "" || seen[cwd] {
			return
		}
		seen[cwd] = true
		others = append(others, cwd)
	}
	dir := filepath.Join(s.home, "sessions")
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sqlite") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			db, err := s.stores.open(path, state.Statements(), state.SchemaVersion)
			if err != nil {
				continue
			}
			rows, err := state.ListSessions(ctx, db, state.ListCap)
			if err != nil || len(rows) == 0 {
				continue
			}
			add(rows[0].Cwd)
		}
	}
	out := make([]string, 0, len(others)+1)
	if s.cwd != "" {
		out = append(out, s.cwd)
		seen[s.cwd] = true
	}
	sort.Strings(others)
	for _, o := range others {
		if !seen[o] {
			out = append(out, o)
			seen[o] = true
		}
	}
	return out
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.readCtx(r)
	defer cancel()
	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		writeErr(w, http.StatusBadRequest, "cwd is required")
		return
	}
	if !s.stateFile(cwd) {
		writeErr(w, http.StatusNotFound, "no such workspace: "+cwd)
		return
	}
	db, err := s.stores.state(cwd)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows, err := state.ListSessions(ctx, db, state.ListCap)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]sessionJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, sessionJSON{
			ID: row.ID, Cwd: row.Cwd,
			Started: row.Started.UTC().Format(time.RFC3339),
			Exit:    row.Exit, Turns: row.Turns,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"cwd": cwd, "sessions": out})
}

func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request, id string) {
	ctx, cancel := s.readCtx(r)
	defer cancel()
	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		writeErr(w, http.StatusBadRequest, "cwd is required")
		return
	}
	if !s.stateFile(cwd) {
		writeErr(w, http.StatusNotFound, "no such workspace: "+cwd)
		return
	}
	db, err := s.stores.state(cwd)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sess, err := state.Resume(ctx, db, id)
	if err != nil {
		if errors.Is(err, state.ErrNoSuchSession) {
			writeErr(w, http.StatusNotFound, "no such session: "+id)
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	usage, err := state.SessionUsage(ctx, db, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	limit, offset := pageParams(r)
	total := len(sess.Messages)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := sess.Messages[offset:end]
	msgs := make([]messageJSON, 0, len(page))
	for _, m := range page {
		msgs = append(msgs, messageJSONOf(m))
	}
	urows := make([]usageJSON, 0, len(usage))
	for _, u := range usage {
		urows = append(urows, usageJSON{
			Seq: u.Seq, Prompt: u.Prompt, Completion: u.Completion,
			CacheRead: u.CacheRead, CacheWrite: u.CacheWrite,
		})
	}
	writeJSON(w, http.StatusOK, transcriptJSON{
		ID: id, Cwd: cwd, Total: total, Limit: limit, Offset: offset,
		HasMore:  end < total,
		Messages: msgs, Usage: urows,
	})
}

func (s *Server) handleTodoRead(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.readCtx(r)
	defer cancel()
	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		writeErr(w, http.StatusBadRequest, "cwd is required")
		return
	}
	db, err := s.stores.todo(cwd)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	proj := todoProject(cwd)
	var (
		text string
	)
	if r.URL.Query().Get("all") == "true" {
		text, err = todostore.ReadAll(ctx, db, proj, sessionName)
	} else {
		text, err = todostore.Read(ctx, db, proj, sessionName)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cwd": cwd, "text": text})
}

func (s *Server) handleScheduler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.readCtx(r)
	defer cancel()
	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		writeErr(w, http.StatusBadRequest, "cwd is required")
		return
	}
	sdb, err := s.stores.scheduler()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	text, err := sched.List(ctx, sdb, s.crontab, cwd, nil, time.Now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	worker := ""
	if s.workers != nil {
		worker = s.workers.Model
	}
	writeJSON(w, http.StatusOK, map[string]any{"cwd": cwd, "text": text, "worker": worker})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	ids := s.models.Known()
	rows := make([]modelJSON, 0, len(ids))
	for _, id := range ids {
		m, ok := s.models.Get(id)
		if !ok {
			continue
		}
		efforts := m.Efforts
		if efforts == nil {
			efforts = []string{}
		}
		rows = append(rows, modelJSON{
			ID: m.ID, Window: m.Window, MaxTokens: m.MaxTokens,
			Reserve: m.Reserve, KeepRecent: m.KeepRecent,
			Role: m.Role, Effort: m.Effort, Efforts: efforts,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": rows})
}

func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	loaded, pending, disabled, err := listPlugins(s.home)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"loaded":   pluginRows(loaded),
		"pending":  pluginRows(pending),
		"disabled": pluginRows(disabled),
	})
}

func pluginRows(ps []Plugin) []pluginJSON {
	out := make([]pluginJSON, 0, len(ps))
	for _, p := range ps {
		out = append(out, pluginJSON{Name: p.Name, Description: p.Description, File: p.File})
	}
	return out
}

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
	items := make([]todostore.CreateItem, 0, len(lines))
	for _, l := range lines {
		items = append(items, todostore.CreateItem{Text: l})
	}
	db, err := s.stores.todo(cwd)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	reply, err := todostore.Create(ctx, db, todoProject(cwd), items, sessionName)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
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
	if s.workers == nil {
		writeErr(w, http.StatusBadRequest, "scheduler: no workers configured ("+filepath.Join(s.home, "workers.json")+" names the model)")
		return
	}
	if in.Model == "" {
		in.Model = s.workers.Model
	}
	sdb, err := s.stores.scheduler()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	reply, err := sched.Create(ctx, sdb, s.crontab,
		in, cwd, sessionName, s.runnerCmd, time.Now)
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

func (s *Server) serveStatic(w http.ResponseWriter, name, ct string) {
	data, err := fs.ReadFile(s.static, name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(data)
}

func (s *Server) serveStaticFile(w http.ResponseWriter, r *http.Request, name string) {
	if name == "" || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(s.static, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mimeFor(name))
	_, _ = w.Write(data)
}

func (s *Server) readCtx(r *http.Request) (context.Context, context.CancelFunc) {
	to := s.readTO
	if to <= 0 {
		to = defaultReadTO
	}
	return context.WithTimeout(r.Context(), to)
}

func (s *Server) stateFile(cwd string) bool {
	_, err := os.Stat(state.StorePath(s.home, cwd))
	return err == nil
}

func (s *Server) originOK(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	for _, allowed := range s.origins {
		if strings.EqualFold(o, allowed) {
			return true
		}
	}
	return strings.EqualFold(o, requestFront(r))
}

func requestFront(r *http.Request) string {
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return ""
	}
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + host
}

func pageParams(r *http.Request) (limit, offset int) {
	limit, offset = defaultPage, 0
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
			if limit > maxPage {
				limit = maxPage
			}
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

func splitTasks(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func mimeFor(name string) string {
	switch {
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func setOf(methods ...string) map[string]bool {
	out := make(map[string]bool, len(methods))
	for _, m := range methods {
		out[m] = true
	}
	return out
}

func methods(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

type sessionJSON struct {
	ID      string `json:"id"`
	Cwd     string `json:"cwd"`
	Started string `json:"started"`
	Exit    string `json:"exit"`
	Turns   int    `json:"turns"`
}

type messageJSON struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Reasoning string         `json:"reasoning,omitempty"`
	ToolCalls []toolCallJSON `json:"tool_calls,omitempty"`
	ToolID    string         `json:"tool_id,omitempty"`
}

type toolCallJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

type usageJSON struct {
	Seq        int64 `json:"seq"`
	Prompt     int64 `json:"prompt"`
	Completion int64 `json:"completion"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

type transcriptJSON struct {
	ID       string        `json:"id"`
	Cwd      string        `json:"cwd"`
	Total    int           `json:"total"`
	Limit    int           `json:"limit"`
	Offset   int           `json:"offset"`
	HasMore  bool          `json:"has_more"`
	Messages []messageJSON `json:"messages"`
	Usage    []usageJSON   `json:"usage"`
}

type modelJSON struct {
	ID         string   `json:"id"`
	Window     int      `json:"window"`
	MaxTokens  int      `json:"max_tokens"`
	Reserve    int      `json:"reserve"`
	KeepRecent int      `json:"keep_recent"`
	Role       string   `json:"role"`
	Effort     string   `json:"effort"`
	Efforts    []string `json:"efforts"`
}

type pluginJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	File        string `json:"file"`
}

func messageJSONOf(m core.Message) messageJSON {
	out := messageJSON{
		Role:      string(m.Role),
		Content:   m.Content,
		Reasoning: m.Reasoning,
		ToolID:    m.ToolID,
	}
	if len(m.ToolCalls) > 0 {
		out.ToolCalls = make([]toolCallJSON, 0, len(m.ToolCalls))
		for _, c := range m.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, toolCallJSON{ID: c.ID, Name: c.Name, Args: string(c.Args)})
		}
	}
	return out
}

func todoProject(cwd string) todostore.Project {
	return todostore.Project{Key: scope.Key(cwd), Label: scope.Label(cwd)}
}
