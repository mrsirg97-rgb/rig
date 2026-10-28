package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/mrsirg97-rgb/rig/v3/webtools"
)

const webToolsBodyCap = 4096

func WithTools(tools webtools.Tools) Option {
	return func(o *options) { o.tools = tools }
}

type webToolsReply struct {
	webtools.Status
	Lines []string `json:"lines"`
}

func noToolsErr() string {
	return "webtools: no web tools (the root did not wire webtools.WithTools)"
}

func (s *server) handleWebTools(w http.ResponseWriter, r *http.Request) {
	if s.tools == nil {
		writeErr(w, http.StatusNotImplemented, noToolsErr())
		return
	}
	st, err := s.tools.Status(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": webToolsReply{Status: st, Lines: st.Lines()}})
}

func (s *server) handleWebToolsVerb(w http.ResponseWriter, r *http.Request, verb string) {
	if s.tools == nil {
		writeErr(w, http.StatusNotImplemented, noToolsErr())
		return
	}
	var in struct {
		Password string `json:"password"`
		Force    bool   `json:"force"`
	}
	if r.Body != nil {
		dec := json.NewDecoder(io.LimitReader(r.Body, webToolsBodyCap))
		if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			writeErr(w, http.StatusBadRequest, "webtools: the request body is not JSON: "+err.Error())
			return
		}
	}
	var out string
	var err error
	switch verb {
	case "generate":
		out, err = s.tools.Generate(r.Context(), in.Password, in.Force)
	case "start":
		out, err = s.tools.Start(r.Context())
	case "stop":
		out, err = s.tools.Stop(r.Context())
	default:
		writeErr(w, http.StatusNotFound, "webtools: "+verb+" is not a verb (generate, start, stop)")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"out": out})
}

func webToolsVerbOf(path string) string {
	return strings.TrimPrefix(path, "/api/webtools/")
}
