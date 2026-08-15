package core

import "context"

// Frontend is the human-facing seam: a blocking pull for input and a
// fire-and-forget observation of the turn stream. The CLI implements it
// over stdin/stdout; a TUI or programmatic caller implements the same two
// methods later.
type Frontend interface {
	Input(ctx context.Context) (string, error)
	Notify(ev Event)
}
