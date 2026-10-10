package web

import (
	"net/http"
	"regexp"
	"strings"
	"time"
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
	case path == "/api/todo/complete" || path == "/api/todo/retry":
		return setOf("POST"), true
	case path == "/api/scheduler":
		return setOf("GET", "POST"), true
	case path == "/api/scheduler/pause" || path == "/api/scheduler/resume" ||
		path == "/api/scheduler/remove" || path == "/api/scheduler/update" ||
		path == "/api/scheduler/repair":
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
	case path == "/api/chat":
		return setOf("POST"), true
	case path == "/api/chat/events":
		return setOf("GET"), true
	case path == "/api/chat/answer" || path == "/api/chat/interrupt":
		return setOf("POST"), true
	case path == "/api/status" || path == "/api/swarm":
		return setOf("GET"), true
	}
	return nil, false
}

func (s *Server) dispatch(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case path == "/":
		s.serveStatic(w, "index.html", "text/html; charset=utf-8")
	case path == "/static/manifest.webmanifest":
		s.serveManifest(w, r)
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
	case path == "/api/scheduler/repair":
		s.handleSchedulerRepair(w, r)
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
	case path == "/api/chat":
		s.handleChatSend(w, r)
	case path == "/api/chat/events":
		s.handleChatEvents(w, r)
	case path == "/api/chat/answer":
		s.handleChatAnswer(w, r)
	case path == "/api/chat/interrupt":
		s.handleChatInterrupt(w, r)
	case path == "/api/status":
		s.handleStatus(w, r)
	case path == "/api/swarm":
		s.handleSwarm(w, r)
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
