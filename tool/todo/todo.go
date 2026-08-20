package todo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

const schemaJSON = `{
	"type": "object",
	"required": ["action"],
	"properties": {
		"action": {
			"enum": ["create", "start", "complete", "fail", "retry", "move", "read"],
			"description": "The action to perform. Required."
		},
		"tasks": {
			"type": "array",
			"description": "Full replacement queue. Required when action='create'.",
			"items": {
				"type": "object",
				"required": ["text"],
				"properties": {
					"text": {
						"type": "string",
						"description": "What needs doing"
					},
					"dependsOn": {
						"type": ["string", "null"],
						"description": "Task id (tN) or exact text this task depends on; null clears the link"
					}
				}
			}
		},
		"id": {
			"type": "string",
			"description": "Task id as shown by the tool. Required for start/complete/fail/retry."
		},
		"pos": {
			"type": "integer",
			"minimum": 1,
			"description": "Queue position (1-based, first = 1) for action='move'."
		},
		"all": {
			"type": "boolean",
			"description": "read all:true returns the full history (done rows included); the default read is the actionable queue."
		}
	}
}`

const description = "Task queue per working directory. action REQUIRED. create replaces the queue (tasks: [{text}]); " +
	"start/complete/fail/retry transition one task by id; read prints it. " +
	"pending -> in_progress -> done (read-only) or failed; failed -> retry -> pending. " +
	"create may link tasks into a tree: tasks[].dependsOn (task id or exact text; null clears a link); " +
	"a task is blocked until its dependency is done; cycles are refused; next skips blocked tasks. " +
	"several tasks may be in flight; batched transitions apply in order, each against fresh state. " +
	"move reorders the queue: action='move' id + pos (1-based queue position); positions are " +
	"minted as events, never updated in place. " +
	"events record the claiming session; start claims, complete by a foreign session refuses " +
	"(fail it first to take over), fail frees the claim; completing your own unclaimed " +
	"pending task implicitly starts and completes it (the echo notes auto-started). " +
	"the event log auto-compacts past 1000 events: a full-state snapshot replaces history " +
	"(staleness epochs reset), replay stays exact. " +
	"the model's own read is the contract: transitions return the affected row and the summary; " +
	"read returns the actionable queue (done folds into the summary line); read all:true returns the history. " +
	"ids are minted by the tool; copy, never invent."

type adapter struct{ db store.DB }

func New(db store.DB) core.Tool { return adapter{db: db} }

func (a adapter) Name() string        { return "todo" }
func (a adapter) Description() string { return description }
func (a adapter) Schema() json.RawMessage {
	return json.RawMessage(schemaJSON)
}

type given struct {
	Action string           `json:"action"`
	Tasks  []map[string]any `json:"tasks"`
	ID     string           `json:"id"`
	Pos    *int             `json:"pos"`
	All    *bool            `json:"all"`
}

func (a adapter) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var g given
	if err := json.Unmarshal(args, &g); err != nil {
		return "", fmt.Errorf("todo: %v", err)
	}
	session := ""
	if s, ok := core.SessionFrom(ctx); ok && s != nil {
		session = s.ID
	}
	switch g.Action {
	case "":
		return "", fmt.Errorf("todo: action required")
	case "create":
		items, err := itemsOf(g.Tasks)
		if err != nil {
			return "", err
		}
		return todostore.Create(ctx, a.db, items, session)
	case "start", "complete", "fail", "retry":
		if g.ID == "" {
			return "", fmt.Errorf("action '%s' requires id", g.Action)
		}
		switch g.Action {
		case "start":
			return todostore.Start(ctx, a.db, g.ID, session)
		case "complete":
			return todostore.Complete(ctx, a.db, g.ID, session)
		case "fail":
			return todostore.Fail(ctx, a.db, g.ID, session)
		default:
			return todostore.Retry(ctx, a.db, g.ID, session)
		}
	case "move":
		if g.ID == "" {
			return "", fmt.Errorf("action 'move' requires id")
		}
		if g.Pos == nil {
			return "", fmt.Errorf("action 'move' requires pos")
		}
		return todostore.Move(ctx, a.db, g.ID, *g.Pos, session)
	case "read":
		if g.All != nil && *g.All {
			return todostore.ReadAll(ctx, a.db, session)
		}
		return todostore.Read(ctx, a.db, session)
	default:
		return "", fmt.Errorf("todo: unknown action %q", g.Action)
	}
}

func itemsOf(tasks []map[string]any) ([]todostore.CreateItem, error) {
	if tasks == nil {
		return nil, fmt.Errorf("action 'create' requires tasks: array of {text}")
	}
	var items []todostore.CreateItem
	for _, raw := range tasks {
		var item todostore.CreateItem
		text, ok := raw["text"].(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("todo: tasks[].text required")
		}
		item.Text = text
		if v, present := raw["dependsOn"]; present {
			switch dep := v.(type) {
			case nil:
				item.DepNull = true
			case string:
				item.DependsOn = &dep
			default:
				return nil, fmt.Errorf("todo: tasks[].dependsOn must be a task id, exact text, or null")
			}
		}
		items = append(items, item)
	}
	return items, nil
}
