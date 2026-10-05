package todo

import (
	"context"
	"errors"
	"fmt"

	"github.com/mrsirg97-rgb/rig/v2/core"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

const workerBoardRefusal = "todo: the supervisor owns the board; a worker does not claim, start, complete, fail, accept, or reject its entries (findings go in note and rem)"

func sessionOf(ctx context.Context) string {
	if s, ok := core.SessionFrom(ctx); ok && s != nil {
		return s.ID
	}
	return ""
}

func (a todo) board() error {
	if bool(a.mode) {
		return errors.New(workerBoardRefusal)
	}
	return nil
}

func withID(action, id string) error {
	if id == "" {
		return fmt.Errorf("action '%s' requires id", action)
	}
	return nil
}

func (a todo) Create(ctx context.Context, scope string, item todostore.CreateItem) (string, error) {
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	reply, err := todostore.Create(ctx, a.db, p, item, sessionOf(ctx))
	if err != nil {
		return reply, err
	}
	a.wakeRouter()
	return reply, nil
}

func (a todo) Claim(ctx context.Context, scope, status string) (string, error) {
	if err := a.board(); err != nil {
		return "", err
	}
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	return todostore.Claim(ctx, a.db, p, sessionOf(ctx), status)
}

func (a todo) Start(ctx context.Context, scope, id string) (string, error) {
	if err := a.board(); err != nil {
		return "", err
	}
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	if err := withID("start", id); err != nil {
		return "", err
	}
	return todostore.Start(ctx, a.db, p, id, sessionOf(ctx), bool(a.mode))
}

func (a todo) Complete(ctx context.Context, scope, id string) (string, error) {
	if err := a.board(); err != nil {
		return "", err
	}
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	if err := withID("complete", id); err != nil {
		return "", err
	}
	reply, err := todostore.Complete(ctx, a.db, p, id, sessionOf(ctx), bool(a.mode))
	if err != nil {
		return reply, err
	}
	a.wakeRouter()
	return reply, nil
}

func (a todo) Fail(ctx context.Context, scope, id string) (string, error) {
	if err := a.board(); err != nil {
		return "", err
	}
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	if err := withID("fail", id); err != nil {
		return "", err
	}
	return todostore.Fail(ctx, a.db, p, id, sessionOf(ctx), bool(a.mode))
}

func (a todo) Release(ctx context.Context, scope, id string) (string, error) {
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	if err := withID("release", id); err != nil {
		return "", err
	}
	return todostore.Release(ctx, a.db, p, id, sessionOf(ctx))
}

func (a todo) Retry(ctx context.Context, scope, id string) (string, error) {
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	if err := withID("retry", id); err != nil {
		return "", err
	}
	return todostore.Retry(ctx, a.db, p, id, sessionOf(ctx))
}

func (a todo) Move(ctx context.Context, scope, id string, pos int) (string, error) {
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	if err := withID("move", id); err != nil {
		return "", err
	}
	return todostore.Move(ctx, a.db, p, id, pos, sessionOf(ctx))
}

func (a todo) Prune(ctx context.Context, scope string) (string, error) {
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	return todostore.Prune(ctx, a.db, p, sessionOf(ctx))
}

func (a todo) Read(ctx context.Context, scope string) (string, error) {
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	return todostore.Read(ctx, a.db, p, sessionOf(ctx))
}

func (a todo) ReadAll(ctx context.Context, scope string) (string, error) {
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	return todostore.ReadAll(ctx, a.db, p, sessionOf(ctx))
}

func (a todo) ReadOne(ctx context.Context, scope, id string) (string, error) {
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	return todostore.ReadOne(ctx, a.db, p, id, sessionOf(ctx))
}

func (a todo) Finished(ctx context.Context, scope string, n int) (string, error) {
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	return todostore.ReadFinished(ctx, a.db, p, sessionOf(ctx), n)
}

func (a todo) Note(ctx context.Context, scope, id, text string) (string, error) {
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	if err := withID("note", id); err != nil {
		return "", err
	}
	if text == "" {
		return "", fmt.Errorf("action 'note' requires note text")
	}
	return todostore.Note(ctx, a.db, p, id, text, sessionOf(ctx))
}

func (a todo) Notes(ctx context.Context, scope, id string) (string, error) {
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	if err := withID("notes", id); err != nil {
		return "", err
	}
	return todostore.Notes(ctx, a.db, p, id, sessionOf(ctx))
}

func (a todo) Accept(ctx context.Context, scope, id string) (string, error) {
	if err := a.board(); err != nil {
		return "", err
	}
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	if err := withID("accept", id); err != nil {
		return "", err
	}
	return todostore.Accept(ctx, a.db, p, id, sessionOf(ctx))
}

func (a todo) Reject(ctx context.Context, scope, id, reason string) (string, error) {
	if err := a.board(); err != nil {
		return "", err
	}
	p, err := resolve(scope)
	if err != nil {
		return "", err
	}
	if err := withID("reject", id); err != nil {
		return "", err
	}
	if reason == "" {
		return "", fmt.Errorf("action 'reject' requires a reason")
	}
	return todostore.Reject(ctx, a.db, p, id, reason, sessionOf(ctx))
}
