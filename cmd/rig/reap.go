package main

import (
	"context"
	"fmt"

	"github.com/mrsirg97-rgb/rig/v3/store"
	"github.com/mrsirg97-rgb/rig/v3/store/state"
	todostore "github.com/mrsirg97-rgb/rig/v3/store/todo"
)

func sessionQueue(ctx context.Context, sdb store.DB, cwd, session string) (todostore.Project, error) {
	if session != "" {
		rows, err := state.ListSessions(ctx, sdb, state.ListCap)
		if err != nil {
			return todostore.ProjectOf(cwd), fmt.Errorf("the session's recorded queue binding: %w", err)
		}
		for _, row := range rows {
			if row.ID == session && row.Cwd != "" {
				return todostore.ProjectOf(row.Cwd), nil
			}
		}
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
