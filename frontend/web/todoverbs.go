package web

import (
	"net/http"
	"regexp"
	"strings"

	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

var todoIDRe = regexp.MustCompile(`^t\d{1,9}$`)

func (s *Server) handleTodoVerb(w http.ResponseWriter, r *http.Request, verb string) {
	ctx, cancel := s.readCtx(r)
	defer cancel()
	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		writeErr(w, http.StatusBadRequest, "cwd is required")
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if !s.writeBody(w, r, &in) {
		return
	}
	id := strings.TrimSpace(in.ID)
	if !todoIDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "a task id as the tool shows it (tN) is required")
		return
	}
	db, err := s.stores.todo(cwd)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var reply string
	switch verb {
	case "start":
		reply, err = todostore.Start(ctx, db, id, sessionName)
	case "complete":
		reply, err = todostore.Complete(ctx, db, id, sessionName)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cwd": cwd, "reply": reply})
}
