package main

import (
	"context"
	"errors"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/frontend/tui"
	"github.com/mrsirg97-rgb/rig/v2/frontend/web"
	"github.com/mrsirg97-rgb/rig/v2/store"
)

func tuiStatusIn(r *root, db store.DB) func(context.Context) tui.StatusIn {
	return func(ctx context.Context) tui.StatusIn {
		eff := r.effort
		if eff == "" {
			eff = r.row.Effort
		}
		b := tui.StatusIn{Model: r.activeID, Effort: eff, Window: r.row.Window, Role: r.role, Approve: r.approve}
		if r.workers != nil {
			b.Workers = r.workers.Model
		}
		if r.session == nil {
			return b
		}
		b.Session = r.session.ID
		if err := db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(u.prompt), 0), COALESCE(SUM(u.completion), 0), COALESCE(SUM(u.cache_read), 0), COALESCE(SUM(u.cost), 0)
			 FROM usage u JOIN messages m ON m.seq = u.message_seq
			 WHERE m.session_id = ?`, r.session.ID).Scan(&b.Up, &b.Down, &b.CacheRead, &b.Cost); err != nil {
			return b
		}
		return b
	}
}

func webStatus(r *root, db store.DB) func(context.Context) web.Status {
	in := tuiStatusIn(r, db)
	return func(ctx context.Context) web.Status {
		s := in(ctx)
		return web.Status{
			Model: s.Model, Effort: s.Effort, Window: s.Window, Role: s.Role, Approve: s.Approve,
			Workers: s.Workers, Session: s.Session, Up: s.Up, Down: s.Down, CacheRead: s.CacheRead,
			Cost: s.Cost, Rows: s.Rows,
		}
	}
}

func sessionFor(resumeID string, resume func(id string) (*core.Session, error)) (*core.Session, error) {
	if resumeID == "" {
		return core.NewSession(), nil
	}
	s, err := resume(resumeID)
	if err != nil {
		return nil, err
	}
	if s == nil || s.ID == "" {
		return nil, errors.New("resume: the projection returned no session")
	}
	return s, nil
}
