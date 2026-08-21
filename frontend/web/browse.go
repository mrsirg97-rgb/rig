package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxBrowseEntries = 500

type browseEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	if s.root == "" {
		writeErr(w, http.StatusInternalServerError, "no browse root (no home directory)")
		return
	}
	root, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "browse root: "+err.Error())
		return
	}
	req := r.URL.Query().Get("path")
	if req == "" {
		req = root
	}
	abs, err := filepath.Abs(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "no such directory: "+req)
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		writeErr(w, http.StatusForbidden, "outside the browse root "+root+": "+resolved)
		return
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such directory: "+req)
		return
	}
	if !fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "not a directory: "+resolved)
		return
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	hidden := r.URL.Query().Get("hidden") == "true"
	dirs := make([]browseEntry, 0)
	truncated := false
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !hidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if len(dirs) == maxBrowseEntries {
			truncated = true
			break
		}
		dirs = append(dirs, browseEntry{Name: e.Name(), Path: filepath.Join(resolved, e.Name())})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	parent := ""
	if resolved != root {
		parent = filepath.Dir(resolved)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"root": root, "path": resolved, "parent": parent, "dirs": dirs, "truncated": truncated,
	})
}
