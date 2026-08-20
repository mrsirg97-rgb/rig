package core

import "context"

type Frontend interface {
	Input(ctx context.Context) (string, error)
	Notify(ev Event)
}
