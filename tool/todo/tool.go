package todo

import (
	"context"
	"encoding/json"

	"github.com/mrsirg97-rgb/rig/v2/store"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
	"github.com/mrsirg97-rgb/rig/v2/tool"
)

type Mode bool

const (
	Interactive Mode = false
	Worker      Mode = true
)

type Todo interface {
	tool.Definition
	Exec(ctx context.Context, args json.RawMessage) (string, error)

	Create(ctx context.Context, scope string, item todostore.CreateItem) (string, error)
	Claim(ctx context.Context, scope, status string) (string, error)
	Complete(ctx context.Context, scope, id string) (string, error)
	Fail(ctx context.Context, scope, id string) (string, error)
	Release(ctx context.Context, scope, id string) (string, error)
	Retry(ctx context.Context, scope, id string) (string, error)
	Move(ctx context.Context, scope, id string, pos int) (string, error)
	Prune(ctx context.Context, scope string) (string, error)
	Read(ctx context.Context, scope string) (string, error)
	ReadAll(ctx context.Context, scope string) (string, error)
	ReadOne(ctx context.Context, scope, id string) (string, error)
	Finished(ctx context.Context, scope string, n int) (string, error)
	Note(ctx context.Context, scope, id, text string) (string, error)
	Notes(ctx context.Context, scope, id string) (string, error)
	Accept(ctx context.Context, scope, id string) (string, error)
	Reject(ctx context.Context, scope, id, reason string) (string, error)
}

type todo struct {
	tool.Definition
	db   store.DB
	mode Mode
	wake func()
}

func New(db store.DB, mode Mode, wake ...func()) Todo {
	a := todo{Definition: tool.Def("todo"), db: db, mode: mode}
	if len(wake) > 0 {
		a.wake = wake[0]
	}
	return a
}

func (a todo) wakeRouter() {
	if a.wake != nil {
		a.wake()
	}
}
