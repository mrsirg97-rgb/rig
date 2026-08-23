package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/scope"
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
		},
		"project": {
			"type": "string",
			"description": "the queue's project (default: this directory's repo)"
		}
	}
}`

const description = "the task queue for this working directory. Guidelines: any job of three or more steps -> " +
	"create before the first edit (tasks: [{text, dependsOn?}]), start before working, complete or fail on " +
	"finish; read shows the actionable queue (all:true for history); move reorders by a 1-based pos. Reply: " +
	"the affected row and the summary; a refusal names the rule. Ids (tN) are minted by the tool — copy, never invent."

type adapter struct{ db store.DB }

func New(db store.DB) core.Tool { return adapter{db: db} }

func (a adapter) Name() string        { return "todo" }
func (a adapter) Description() string { return description }
func (a adapter) Schema() json.RawMessage {
	return json.RawMessage(schemaJSON)
}

type given struct {
	Action  string           `json:"action"`
	Tasks   []map[string]any `json:"tasks"`
	ID      string           `json:"id"`
	Pos     *int             `json:"pos"`
	All     *bool            `json:"all"`
	Project *string          `json:"project"`
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
	cwd := ""
	if g.Project != nil && *g.Project != "" {
		cwd = *g.Project
	} else if wd, err := os.Getwd(); err == nil {
		cwd = wd
	}
	p := todostore.Project{Key: scope.Key(cwd), Label: scope.Label(cwd)}
	switch g.Action {
	case "":
		return "", fmt.Errorf("todo: action required")
	case "create":
		items, err := itemsOf(g.Tasks)
		if err != nil {
			return "", err
		}
		return todostore.Create(ctx, a.db, p, items, session)
	case "start", "complete", "fail", "retry":
		if g.ID == "" {
			return "", fmt.Errorf("action '%s' requires id", g.Action)
		}
		switch g.Action {
		case "start":
			return todostore.Start(ctx, a.db, p, g.ID, session)
		case "complete":
			return todostore.Complete(ctx, a.db, p, g.ID, session)
		case "fail":
			return todostore.Fail(ctx, a.db, p, g.ID, session)
		default:
			return todostore.Retry(ctx, a.db, p, g.ID, session)
		}
	case "move":
		if g.ID == "" {
			return "", fmt.Errorf("action 'move' requires id")
		}
		if g.Pos == nil {
			return "", fmt.Errorf("action 'move' requires pos")
		}
		return todostore.Move(ctx, a.db, p, g.ID, *g.Pos, session)
	case "read":
		if g.All != nil && *g.All {
			return todostore.ReadAll(ctx, a.db, p, session)
		}
		return todostore.Read(ctx, a.db, p, session)
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
