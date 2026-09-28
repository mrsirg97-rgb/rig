package web

import (
	"context"
	"encoding/json"
	"github.com/mrsirg97-rgb/rig/v2/store/state"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

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
	case strings.HasSuffix(name, ".webmanifest"):
		return "application/manifest+json"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
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
