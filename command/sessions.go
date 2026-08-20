package command

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
)

type sessionsCmd struct{}

func (sessionsCmd) Sub() []Sub {
	return []Sub{
		{Name: "list", Desc: "show the sessions"},
		{Name: "show", Desc: "show a session's transcript: show <id>"},
		{Name: "resume", Desc: "resume a session: resume <id>"},
	}
}

func (sessionsCmd) Name() string { return "sessions" }

func (sessionsCmd) Description() string {
	return "list, show, or resume the workspace sessions"
}

func (sessionsCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(args)
	switch {
	case len(fields) == 0 || (len(fields) == 1 && fields[0] == "list"):
		if e.SessionList == nil {
			return "", errors.New("sessions: no sessions seam (the root did not wire one)")
		}
		rows, err := e.SessionList(ctx)
		if err != nil {
			return "", err
		}
		if len(rows) == 0 {
			return "sessions: none", nil
		}
		return renderList(rows), nil
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
	default:
		return "", errors.New("sessions: usage: sessions [list|show|resume <id>]")
	}
}

func renderList(rows []SessionRow) string {
	w := 0
	for _, r := range rows {
		if len(r.ID) > w {
			w = len(r.ID)
		}
	}
	var b strings.Builder
	for _, r := range rows {
		mark := ""
		if r.Current {
			mark = "  *"
		}
		fmt.Fprintf(&b, "%-*s  started %s  exit %-6s turns %d%s\n",
			w, r.ID, r.Started.Format(time.RFC3339), r.Exit, r.Turns, mark)
	}
	return b.String()
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
