package decision

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

const siteStateCap = 4096

var riskQuestion = Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous")

type siteLink struct {
	proposer Proposer
}

func Site(p Proposer) core.ToolMiddleware {
	return &siteLink{proposer: p}
}

func (s *siteLink) Wrap(next core.ToolExec) core.ToolExec {
	return func(ctx context.Context, call core.ToolCall) (string, error) {
		content, err := next(ctx, call)
		if call.Name == "bash" && s.proposer != nil {
			s.propose(ctx, call, err)
		}
		return content, err
	}
}

func (s *siteLink) propose(ctx context.Context, call core.ToolCall, err error) {
	var a struct {
		Command   string `json:"command"`
		Workspace string `json:"workspace"`
	}
	_ = json.Unmarshal(call.Args, &a)
	state := map[string]string{
		"command": a.Command,
	}
	if a.Workspace != "" {
		state["workspace"] = a.Workspace
	}
	if err != nil {
		state["error"] = err.Error()
	}
	b, mErr := json.Marshal(state)
	if mErr != nil {
		return
	}
	session := ""
	if sess, ok := core.SessionFrom(ctx); ok {
		session = sess.ID
	}
	s.proposer.Propose(Pending{
		Site:     SiteBash,
		Session:  session,
		State:    truncate(string(b), siteStateCap),
		Question: riskQuestion,
	})
}

func truncate(s string, cap int) string {
	if len(s) <= cap {
		return s
	}
	for cap > 0 && !utf8.RuneStart(s[cap]) {
		cap--
	}
	return s[:cap]
}
