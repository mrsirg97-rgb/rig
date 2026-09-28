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
}

func New(db store.DB, mode Mode) core.Tool { return adapter{db: db, mode: mode} }

func (a adapter) Name() string { return "todo" }

func (a adapter) Description() string { return description }

func (a adapter) Schema() json.RawMessage { return json.RawMessage(schemaJSON) }
