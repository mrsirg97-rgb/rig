package main

import (
	"context"

	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/state"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

var concurrentNatives = map[string]bool{
	"read": true, "view": true,
	"web":      true,
	"delegate": true,
	"decide":   true,
}

var mutatingNatives = map[string]bool{
	"bash": true, "write": true, "edit": true, "python": true,
	"scheduler": true, "plugin": true, "delegate": true,
}

func sessionQueue(ctx context.Context, tdb store.DB, cwd, session string) (todostore.Project, error) {
	b, ok, err := todostore.BindingOf(ctx, tdb, session)
	if err != nil {
		return todostore.ProjectOf(cwd), err
	}
	if ok {
		return b.Project(), nil
	}
	return todostore.ProjectOf(cwd), nil
}

func reapClaims(ctx context.Context, sdb, tdb store.DB, cwd string, proj todostore.Project, session string) (string, error) {
	rows, err := state.ListSessions(ctx, sdb, state.ListCap)
	if err != nil {
		return "", err
	}
	var ended []string
	for _, r := range rows {
		if r.Exit != "" && r.Exit != "open" {
			ended = append(ended, r.ID)
		}
	}
	if len(ended) == 0 {
		return "", nil
	}
	return todostore.Reap(ctx, tdb, proj, ended, session)
}
