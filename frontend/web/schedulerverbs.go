package web

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

var jobIDRe = regexp.MustCompile(`^j\d{1,9}$`)

func (s *Server) handleSchedulerVerb(w http.ResponseWriter, r *http.Request, verb string) {
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
	if !jobIDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "a job id as the tool shows it (jN) is required")
		return
	}
	sdb, err := s.stores.scheduler()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var reply string
	switch verb {
	case "pause":
		reply, err = sched.Pause(ctx, sdb, s.crontab, id, cwd, sessionName)
	case "resume":
		reply, err = sched.Resume(ctx, sdb, s.crontab, id, cwd, sessionName)
	case "remove":
		reply, err = sched.Remove(ctx, sdb, s.crontab, id, cwd, sessionName)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cwd": cwd, "reply": reply})
}

func (s *Server) handleSchedulerUpdate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.readCtx(r)
	defer cancel()
	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		writeErr(w, http.StatusBadRequest, "cwd is required")
		return
	}
	var in sched.UpdateInput
	if !s.writeBody(w, r, &in) {
		return
	}
	in.ID = strings.TrimSpace(in.ID)
	if !jobIDRe.MatchString(in.ID) {
		writeErr(w, http.StatusBadRequest, "a job id as the tool shows it (jN) is required")
		return
	}
	sdb, err := s.stores.scheduler()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	reply, err := sched.Update(ctx, sdb, s.crontab, in, sessionName, s.runnerCmd, time.Now)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cwd": cwd, "reply": reply})
}

func (s *Server) handleSchedulerRuns(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.readCtx(r)
	defer cancel()
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if !jobIDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "a job id as the tool shows it (jN) is required")
		return
	}
	n := 0
	if v := r.URL.Query().Get("n"); v != "" {
		var err error
		n, err = strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeErr(w, http.StatusBadRequest, "n is an integer 1-100 (default 5), got "+strconv.Quote(v))
			return
		}
	}
	sdb, err := s.stores.scheduler()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	text, err := sched.Runs(ctx, sdb, id, n)
	if err != nil {
		if strings.Contains(err.Error(), "no job") {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "text": text})
}
