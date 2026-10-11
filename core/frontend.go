package core

import (
	"context"

	"github.com/mrsirg97-rgb/evt"
)

type Frontend interface {
	Input(ctx context.Context) (string, error)
	Notify(ev evt.Signal)
}
