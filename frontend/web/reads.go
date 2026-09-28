package web

import (
	"context"
	"errors"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v2/store/state"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

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
			cwds, err := state.Cwds(ctx, db)
			if err != nil {
				continue
			}
			for _, cwd := range cwds {
				add(cwd)
			}
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
			Tokens: row.Tokens, Label: row.Label,
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
	text, err := sched.List(ctx, sdb, s.crontab, cwd, s.home, nil, time.Now)
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
