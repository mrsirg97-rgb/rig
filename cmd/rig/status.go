package main

import (
	"context"
	"errors"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/frontend/tui"
	"github.com/mrsirg97-rgb/rig/v3/frontend/web"
	"github.com/mrsirg97-rgb/rig/v3/store"
)

func tuiStatusIn(r *root, db store.DB) func(context.Context) tui.StatusIn {
	return func(context.Context) tui.StatusIn {
		return r.statusNow(db)
	}
}

func (r *root) statusNow(db store.DB) tui.StatusIn {
	eff := r.effort
	if eff == "" {
		eff = r.row.Effort
	}
	b := tui.StatusIn{Model: r.activeID, Effort: eff, Window: r.row.Window, Role: r.role, Approve: r.approve, Workers: "resident"}
	if r.session == nil {
		return b
	}
	b.Session = r.session.ID
	if err := db.QueryRowContext(context.Background(),
		`SELECT COALESCE(SUM(u.prompt), 0), COALESCE(SUM(u.completion), 0), COALESCE(SUM(u.cache_read), 0), COALESCE(SUM(u.cost), 0)
		 FROM usage u JOIN messages m ON m.seq = u.message_seq
		 WHERE m.session_id = ?`, r.session.ID).Scan(&b.Up, &b.Down, &b.CacheRead, &b.Cost); err != nil {
		return b
	}
	return b
}

func (r *root) statusSnapshot(ctx context.Context, db store.DB) (tui.StatusIn, bool) {
	done := make(chan tui.StatusIn, 1)
	r.engine.Add(evt.Func(func(context.Context) { done <- r.statusNow(db) }), rig.PriorityRead)
	select {
	case s := <-done:
		return s, true
	case <-ctx.Done():
		return tui.StatusIn{}, false
	}
}

func webStatus(r *root, db store.DB) func(context.Context) web.Status {
	return func(ctx context.Context) web.Status {
		s, ok := r.statusSnapshot(ctx, db)
		if !ok {
			return web.Status{}
		}
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
