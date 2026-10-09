package todo

import (
	"context"
	"encoding/json"
	"fmt"

	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

type given struct {
	Action   string          `json:"action"`
	Text     string          `json:"text"`
	Requires json.RawMessage `json:"requires"`
	Blocks   json.RawMessage `json:"blocks"`
	ID       string          `json:"id"`
	Pos      *int            `json:"pos"`
	All      *bool           `json:"all"`
	N        *int            `json:"n"`
	Note     string          `json:"note"`
	Status   string          `json:"status"`
	Scope    string          `json:"scope"`
}

func (a todo) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var g given
	if err := json.Unmarshal(args, &g); err != nil {
		return "", fmt.Errorf("todo: %v", err)
	}
	switch g.Action {
	case "":
		return "", fmt.Errorf("todo: action required")
	case "create":
		item, err := itemOf(g)
		if err != nil {
			return "", err
		}
		return a.Create(ctx, g.Scope, item)
	case "claim":
		return a.Claim(ctx, g.Scope, g.Status)
	case "complete":
		return a.Complete(ctx, g.Scope, g.ID)
	case "fail":
		return a.Fail(ctx, g.Scope, g.ID)
	case "release":
		return a.Release(ctx, g.Scope, g.ID)
	case "retry":
		return a.Retry(ctx, g.Scope, g.ID)
	case "move":
		if err := withID("move", g.ID); err != nil {
			return "", err
		}
		if g.Pos == nil {
			return "", fmt.Errorf("action 'move' requires pos")
		}
		return a.Move(ctx, g.Scope, g.ID, *g.Pos)
	case "prune":
		return a.Prune(ctx, g.Scope)
	case "read":
		if g.ID != "" {
			return a.ReadOne(ctx, g.Scope, g.ID)
		}
		if g.All != nil && *g.All {
			return a.ReadAll(ctx, g.Scope)
		}
		return a.Read(ctx, g.Scope)
	case "finished":
		n := 0
		if g.N != nil {
			n = *g.N
		}
		return a.Finished(ctx, g.Scope, n)
	case "note":
		return a.Note(ctx, g.Scope, g.ID, g.Note)
	case "notes":
		return a.Notes(ctx, g.Scope, g.ID)
	case "accept":
		return a.Accept(ctx, g.Scope, g.ID)
	case "reject":
		return a.Reject(ctx, g.Scope, g.ID, g.Note)
	default:
		return "", fmt.Errorf("todo: unknown action %q", g.Action)
	}
}

func itemOf(g given) (todostore.CreateItem, error) {
	item := todostore.CreateItem{Text: g.Text}
	if g.Text == "" {
		return item, fmt.Errorf("action 'create' requires text")
	}
	var err error
	if item.Requires, item.RequiresNull, err = linkOf("requires", g.Requires); err != nil {
		return item, err
	}
	if item.Blocks, item.BlocksNull, err = linkOf("blocks", g.Blocks); err != nil {
		return item, err
	}
	return item, nil
}

func linkOf(key string, raw json.RawMessage) (*string, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	if string(raw) == "null" {
		return nil, true, nil
	}
	var id string
	if json.Unmarshal(raw, &id) != nil {
		return nil, false, fmt.Errorf("todo: %s must be a task id (tN) from a reply, or null", key)
	}
	if id == "" {
		return nil, false, nil
	}
	return &id, false, nil
}
