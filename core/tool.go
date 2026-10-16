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

func Refusal(msg string) (string, error) { return msg, errors.New(msg) }
