package decision

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

const siteStateCap = 4096

var riskQuestion = Question{
	ID:      "risk",
	Kind:    KindChoice,
	Prompt:  "What risk does this bash call carry?",
	Choices: []string{"safe", "changes", "dangerous"},
	Description: map[string]string{
		"safe":      "reads or lists; nothing on disk changes",
		"changes":   "writes only inside the workspace it named",
		"dangerous": "reaches outside the workspace, deletes, or can destroy state",
	},
}

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
		"command": normalize(a.Command),
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

func normalize(command string) string {
	s := strings.Join(strings.Fields(command), " ")
	rest, ok := strings.CutPrefix(s, "cd ")
	if !ok {
		return s
	}
	path, sep, after := cutSeparator(rest)
	if sep == "" || strings.TrimSpace(path) == "" {
		return s
	}
	return strings.TrimSpace(after)
}

func cutSeparator(s string) (before, sep, after string) {
	if i := strings.Index(s, "&&"); i >= 0 {
		if j := strings.IndexByte(s, ';'); j >= 0 && j < i {
			return s[:j], ";", s[j+1:]
		}
		return s[:i], "&&", s[i+2:]
	}
	if j := strings.IndexByte(s, ';'); j >= 0 {
		return s[:j], ";", s[j+1:]
	}
	return s, "", ""
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
