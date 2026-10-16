package core

import (
	"context"
	"encoding/json"
	"errors"
)

type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Exec(ctx context.Context, args json.RawMessage) (string, error)
}

type ToolSpec struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// Refusal is the house shape for a refusal the model must read: the same
// words as content and as error, so the loop lands one and the retry guard
// counts it. The error exists to travel the error channel, not to differ.
func Refusal(msg string) (string, error) { return msg, errors.New(msg) }
