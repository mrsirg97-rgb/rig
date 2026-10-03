package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

type sessionsCmd struct{}

func (sessionsCmd) Sub() []Sub {
	return []Sub{
		{Name: "list", Desc: "show the sessions that fit the screen: list [all|<n>]"},
		{Name: "summary", Desc: "the recent sessions in numbers: turns, models, faults, cache ratio"},
		{Name: "show", Desc: "print a session's transcript: show <id>"},
		{Name: "resume", Desc: "continue a past session here: resume <id>"},
	}
}

func (sessionsCmd) Name() string { return "sessions" }

func (sessionsCmd) Description() string {
	return "this workspace's sessions: list them, see their numbers, print one, or resume one"
}

func (sessionsCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(args)
	switch {
	case len(fields) == 0 || (fields[0] == "list" && len(fields) <= 2):
		if e.SessionList == nil {
			return "", errors.New("sessions: no sessions seam (the root did not wire one)")
		}
		var tail []string
		if len(fields) == 2 {
			tail = fields[1:]
		}
		limit, err := listLimit(e, tail)
		if err != nil {
			return "", fmt.Errorf("sessions: list: %v", err)
		}
		rows, err := e.SessionList(ctx)
		if err != nil {
			return "", err
		}
		if len(rows) == 0 {
			return "sessions: none", nil
		}
		return renderList(rows, limit), nil
	case len(fields) == 1 && fields[0] == "summary":
		if e.Tools == nil {
			return "", errors.New("sessions: no tools seam (the root did not wire one)")
		}
		tool, ok := e.Tools["sessions"]
		if !ok {
			return "", errors.New("sessions: no sessions tool (the root did not put it in Env.Tools)")
		}
		if e.Session != nil {
			if s := e.Session(); s != nil {
				ctx = core.WithSession(ctx, s)
			}
		}
		return tool.Exec(ctx, json.RawMessage(`{"action":"summary"}`))
	case fields[0] == "show" && len(fields) == 2:
		if e.SessionShow == nil {
			return "", errors.New("sessions: no sessions seam (the root did not wire one)")
		}
		out, err := e.SessionShow(ctx, fields[1])
		if err != nil {
			return "", err
		}
		return out, nil
	case fields[0] == "resume" && len(fields) == 2:
		if liveTurn(e) {
			return "", errors.New("sessions: a turn is live; steer or interrupt first")
		}
		if e.Session != nil && fields[1] == e.Session().ID {
			return "", fmt.Errorf("sessions: already the current session: %s", fields[1])
		}
		if e.SessionResume == nil {
			return "", errors.New("sessions: no sessions seam (the root did not wire one)")
		}
		if err := e.SessionResume(ctx, fields[1]); err != nil {
			return "", err
		}
		if e.Steer != nil {
			e.Steer.ClearSlot()
		}
		n := 0
		if e.Session != nil {
			n = len(e.Session().Messages)
		}
		return fmt.Sprintf("sessions: resumed %s (%d messages)", fields[1], n), nil
	}
	switch {
	case len(fields) > 0 && fields[0] == "summary":
		return "", errors.New("sessions: summary takes no args (sessions summary)")
	case len(fields) > 0 && fields[0] == "show":
		if len(fields) == 1 {
			return "", errors.New("sessions: show needs an id (sessions show <id>)")
		}
		return "", errors.New("sessions: show takes one id")
	case len(fields) > 0 && fields[0] == "resume":
		if len(fields) == 1 {
			return "", errors.New("sessions: resume needs an id (sessions resume <id>)")
		}
		return "", errors.New("sessions: resume takes one id")
	case len(fields) > 0 && fields[0] == "list":
		return "", errors.New("sessions: list takes at most a count (sessions list [all|<n>])")
	default:
		return "", errors.New("sessions: usage: sessions [list [all|<n>]|summary|show|resume <id>]")
	}
}

func renderList(rows []SessionRow, limit int) string {
	shown, hidden := fit(len(rows), limit)
	var b strings.Builder
	b.WriteString(plural(len(rows), "session"))
	for _, r := range rows {
		if r.Current {
			b.WriteString(" \u00b7 current " + r.ID)
			break
		}
	}
	for _, r := range rows[:shown] {
		b.WriteString("\n" + row(r.ID, 0, sessionMark(r), plural(r.Turns, "turn"),
			fmt.Sprintf("%d tokens", r.Tokens), "started "+ageOf(r.Started.UTC().Format(time.RFC3339))+" ago", "exit "+r.Exit, r.Label))
	}
	b.WriteString(moreFooter(hidden, "sessions list all"))
	return b.String()
}

func sessionMark(r SessionRow) string {
	switch {
	case r.Current:
		return markActive
	case r.Exit == "open":
		return markIdle
	case r.Exit == "ok":
		return markDone
	default:
		return markFailed
	}
}

func RenderShow(s *core.Session) string {
	var b strings.Builder
	n := 0
	for _, m := range s.Messages {
		n++
		switch m.Role {
		case core.RoleUser:
			fmt.Fprintf(&b, "[%d] user: %s\n", n, m.Content)
		case core.RoleAssistant:
			fmt.Fprintf(&b, "[%d] assistant: %s\n", n, m.Content)
			if m.Reasoning != "" {
				fmt.Fprintf(&b, "    thinking: %s\n", m.Reasoning)
			}
			for _, call := range m.ToolCalls {
				fmt.Fprintf(&b, "    call %s %s %s\n", call.ID, call.Name, string(call.Args))
			}
		case core.RoleTool:
			fmt.Fprintf(&b, "[%d] tool (%s): %s\n", n, m.ToolID, m.Content)
		}
	}
	return b.String()
}
