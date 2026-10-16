package main

import (
	"context"
	"fmt"

	"github.com/mrsirg97-rgb/rig/v3/store"
	"github.com/mrsirg97-rgb/rig/v3/store/state"
	todostore "github.com/mrsirg97-rgb/rig/v3/store/todo"
)

func projectForRow(rows []state.SessionRow, cwd, session string) todostore.Project {
	for _, row := range rows {
		if row.ID == session && row.Cwd != "" {
			return todostore.ProjectOf(row.Cwd)
		}
	}
	return todostore.ProjectOf(cwd)
}

func sessionQueue(ctx context.Context, sdb store.DB, cwd, session string) (todostore.Project, error) {
	rows, err := state.ListSessions(ctx, sdb, state.ListCap)
	if err != nil {
		return todostore.ProjectOf(cwd), fmt.Errorf("the session's recorded queue binding: %w", err)
	}
	return projectForRow(rows, cwd, session), nil
}

func reapClaims(ctx context.Context, tdb store.DB, rows []state.SessionRow, proj todostore.Project, session string) (string, error) {
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
