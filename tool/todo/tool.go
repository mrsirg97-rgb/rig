package todo

import (
	"encoding/json"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/store"
)

type Mode bool

const (
	Interactive Mode = false
	Worker      Mode = true
)

type adapter struct {
	db   store.DB
	mode Mode
	wake func()
}

func New(db store.DB, mode Mode, wake ...func()) core.Tool {
	a := adapter{db: db, mode: mode}
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

func (a adapter) Name() string { return "todo" }

func (a adapter) Description() string { return description }

func (a adapter) Schema() json.RawMessage { return json.RawMessage(schemaJSON) }
