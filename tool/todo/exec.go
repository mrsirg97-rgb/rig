package todo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

type given struct {
	Action string           `json:"action"`
	Tasks  []map[string]any `json:"tasks"`
	ID     string           `json:"id"`
	Pos    *int             `json:"pos"`
	All    *bool            `json:"all"`
	N      *int             `json:"n"`
	Note   string           `json:"note"`
	Status string           `json:"status"`
	Scope  *string          `json:"scope"`
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
	if g.Action == "" {
		return "", fmt.Errorf("todo: action required")
	}
	p, err := resolve(g)
	if err != nil {
		return "", err
	}
	return a.dispatch(ctx, g, p, session)
}
