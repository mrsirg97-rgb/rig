package core

import (
	"context"

	"github.com/mrsirg97-rgb/evt/event"
)

type Frontend interface {
	Input(ctx context.Context) (string, error)
	Notify(ev event.Event)
}
