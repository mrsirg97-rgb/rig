package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

type given struct {
	Action  string           `json:"action"`
	Tasks   []map[string]any `json:"tasks"`
	ID      string           `json:"id"`
	Pos     *int             `json:"pos"`
	All     *bool            `json:"all"`
	N       *int             `json:"n"`
	Note    string           `json:"note"`
	Status  string           `json:"status"`
	Project *string          `json:"project"`
}

func (a adapter) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var g given
	if err := json.Unmarshal(args, &g); err != nil {
		return "", fmt.Errorf("todo: %v", err)
	}
	session := ""
	if s, ok := core.SessionFrom(ctx); ok && s != nil {
		session = s.ID
	}
	if g.Action == "bind" && (g.Project == nil || *g.Project == "") {
		return a.report(ctx, session)
	}
	t, err := a.resolve(ctx, g, session)
	if err != nil {
		return "", err
	}
	if t.source == srcHost && isWrite(g.Action) {
		wd, _ := os.Getwd()
		return "", fmt.Errorf("todo: no project: %s is not a repo, so its queue is shared by every session started there (project: \"~/Projects/x\" binds this session, \"~\" claims this bucket)", wd)
	}
	committed := false
	if g.Action == "bind" {
		committed, err = a.commit(ctx, t)
		if err != nil {
			return "", err
		}
	}
	reply, err := a.dispatch(ctx, g, t.p, session)
	if err != nil {
		return reply, err
	}
	if g.Action != "bind" && t.named && isWrite(g.Action) {
		committed, err = a.commit(ctx, t)
		if err != nil {
			return reply, err
		}
	}
	note := ""
	if committed {
		note = t.note()
	}
	if note == "" {
		return reply, nil
	}
	return "\u2192 " + note + "\n" + reply, nil
}
