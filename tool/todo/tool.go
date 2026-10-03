package todo

import (
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/tool"
)

type Mode bool

const (
	Interactive Mode = false
	Worker      Mode = true
)

type adapter struct {
	tool.Definition
	db   store.DB
	mode Mode
	wake func()
}

func New(db store.DB, mode Mode, wake ...func()) core.Tool {
	a := adapter{Definition: tool.Def("todo"), db: db, mode: mode}
	if len(wake) > 0 {
		a.wake = wake[0]
	}
	return a
}

func (a adapter) wakeRouter() {
	if a.wake != nil {
		a.wake()
	}
}
