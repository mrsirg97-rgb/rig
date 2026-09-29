package todo

import (
	"context"
	"fmt"
	"os"

	"github.com/mrsirg97-rgb/rig/v2/middleware/paths"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

type target struct {
	p       todostore.Project
	session string
	prev    todostore.Binding
	had     bool
	named   bool
	source  string
}

func (t target) commits() bool {
	return t.named && todostore.RealSession(t.session)
}

func (t target) note() string {
	if !t.commits() {
		return ""
	}
	switch {
	case !t.had:
		return "bound to " + t.p.Label
	case t.prev.Scope != t.p.Key:
		return "bound to " + t.p.Label + " (was " + t.prev.Label + ")"
	}
	return ""
}

func (a adapter) commit(ctx context.Context, t target) (bool, error) {
	if !t.commits() {
		return false, nil
	}
	if err := todostore.Bind(ctx, a.db, todostore.Binding{
		Session: t.session, Scope: t.p.Key, Label: t.p.Label, OutsideRepo: t.p.OutsideRepo,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (a adapter) resolve(ctx context.Context, g given, session string) (target, error) {
	raw := g.Project
	named := raw != nil && *raw != ""
	p := todostore.Project{}
	source := ""
	if named {
		dir := paths.Expand(*raw)
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			return target{}, fmt.Errorf("todo: no such project directory: %s", dir)
		}
		p, source = todostore.ProjectOf(dir), srcProject
	} else {
		if todostore.RealSession(session) {
			b, ok, err := todostore.BindingOf(ctx, a.db, session)
			if err != nil {
				return target{}, err
			}
			if ok {
				return target{p: b.Project(), session: session, prev: b, had: true, source: srcBinding}, nil
			}
		}
		wd, err := os.Getwd()
		if err != nil {
			return target{}, fmt.Errorf("todo: no working directory: %v", err)
		}
		p = todostore.ProjectOf(wd)
		source = srcCwd
	}
	t := target{p: p, session: session, named: named, source: source}
	if named && todostore.RealSession(session) {
		prev, had, err := todostore.BindingOf(ctx, a.db, session)
		if err != nil {
			return target{}, err
		}
		t.prev, t.had = prev, had
	}
	return t, nil
}

func (a adapter) report(ctx context.Context, session string) (string, error) {
	t, err := a.resolve(ctx, given{}, session)
	if err != nil {
		return "", err
	}
	p, source := t.p, t.source
	if source == srcBinding {
		return "queue: " + p.Label + " (bound)", nil
	}
	return "queue: " + p.Label + " (this workspace; not bound)", nil
}

func isWrite(action string) bool {
	switch action {
	case "create", "claim", "start", "complete", "fail", "release", "retry", "move", "prune", "note", "accept", "reject":
		return true
	default:
		return false
	}
}
