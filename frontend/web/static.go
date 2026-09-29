package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static
var staticFS embed.FS

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

func (s *Server) serveManifest(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(s.static, "manifest.webmanifest")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	tok, _, err := s.Token()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "serve token: "+err.Error())
		return
	}
	body := strings.Replace(string(data), `"start_url": "/"`, `"start_url": "/?token=`+tok+`"`, 1)
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}
