package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/paths"
	"github.com/mrsirg97-rgb/rig/store"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

const schemaJSON = `{
	"type": "object",
	"required": ["action"],
	"properties": {
		"action": {
			"enum": ["create", "claim", "start", "complete", "fail", "release", "retry", "move", "prune", "bind", "read", "note", "accept", "reject"],
			"description": "The action to perform. Required."
		},
		"tasks": {
			"type": "array",
			"description": "The queue as given: create merges by text, [] clears. Required when action='create'.",
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
			"description": "Task id as shown by the tool. Required for start/complete/fail/release/retry/move/note/accept/reject."
		},
		"note": {
			"type": "string",
			"description": "The note text, or the reason for action='reject'. Required for action='note' and action='reject'."
		},
		"status": {
			"type": "string",
			"enum": ["review"],
			"description": "Optional claim filter: action='claim' with status='review' takes the first task in review for this session."
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
			"description": "the queue's project as a directory: it binds the session, whose later bare verbs then act there (worktree-safe; ~ expands)"
		}
	}
}`

const description = "the task queue for the session's project. Guidelines: any job of three or more steps -> " +
	"create before the first edit (tasks: [{text, dependsOn?}]), claim takes the next pending task whose " +
	"dependency is done, complete lands your task done here (solo); a worker (rig -p: delegate, swarm) is " +
	"read/note-only — the supervisor owns the board, and the worker's findings go in note and rem; accept or " +
	"reject a task in review — the parent's flow is read then accept/reject, an unowned review task " +
	"auto-claims, a foreign hold refuses, and reject takes the reason " +
	"as note; note attaches a message to any task; read shows the actionable queue (all:true for history); " +
	"move reorders by a 1-based pos; prune drops the done rows. Every reply names the queue it acted on " +
	"([rig]); name project when the work is in a repo you did not start in, which binds the session. Reply: " +
	"the affected row and the summary; a refusal names the rule. Ids (tN) are minted by the tool — copy, " +
	"never invent."

// Where a queue's identity came from, named so the tool can tell a write
// it must refuse (a bucket minted from a directory that is not a repo)
// from one it may take silently.
const (
	srcProject = "project"
	srcBinding = "binding"
	srcCwd     = "cwd"
	srcHost    = "host"
)

// Mode says who completes. An interactive session lands its own task
// done in one call (complete+accept, the log stays uniform); a worker
// (rig -p: delegate or swarm) is read/note-only — the supervisor owns
// the board, and the worker's findings go in the task's note and in rem.
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

type given struct {
	Action  string           `json:"action"`
	Tasks   []map[string]any `json:"tasks"`
	ID      string           `json:"id"`
	Pos     *int             `json:"pos"`
	All     *bool            `json:"all"`
	Note    string           `json:"note"`
	Status  string           `json:"status"`
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
	if g.Action == "bind" && (g.Project == nil || *g.Project == "") {
		return a.report(ctx, session)
	}
	t, err := a.resolve(ctx, g, session)
	if err != nil {
		return "", err
	}
	if t.source == srcHost && isWrite(g.Action) {
		wd, _ := os.Getwd()
		return "", fmt.Errorf("todo: no project: %s is not a repo, so its queue is shared by every session started there (project: \"~/Projects/x\" binds this session, \"~\" claims this bucket)", wd)
	}
	// A read that names a project is a peek: it looks at that queue and
	// leaves the session where it was. `bind` is the declaration, so it
	// records whatever the read that follows does. A write records the
	// move only once the action succeeded: a call that changed nothing
	// changes no one's queue, and the refusal already named the queue it
	// tried (`no task 't99' in loom`).
	committed := false
	if g.Action == "bind" {
		committed, err = a.commit(ctx, t)
		if err != nil {
			return "", err
		}
	}
	reply, err := a.dispatch(ctx, g, t.p, session)
	if err != nil {
		return reply, err
	}
	if g.Action != "bind" && t.named && isWrite(g.Action) {
		committed, err = a.commit(ctx, t)
		if err != nil {
			return reply, err
		}
	}
	// The note is a record of the record: it speaks only when the binding
	// row was actually written. A named read is a peek and commits nothing,
	// so it announces no move it did not make.
	note := ""
	if committed {
		note = t.note()
	}
	if note == "" {
		return reply, nil
	}
	return "\u2192 " + note + "\n" + reply, nil
}

// commit records the binding the call resolved to and reports whether it
// wrote: false when the session cannot hold one, true once the row says
// so. The note that follows it is the whole story of the move: silent
// when nothing moved.
func (a adapter) commit(ctx context.Context, t target) (bool, error) {
	if !t.commits() {
		return false, nil
	}
	if err := todostore.Bind(ctx, a.db, todostore.Binding{
		Session: t.session, Scope: t.p.Key, Label: t.p.Label, OutsideRepo: t.p.OutsideRepo,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// workerBoardRefusal is the Worker-mode board door: the six
// board-transition verbs refuse here at the tool's seam, so the store's
// own arms (the swarm controller calls the store directly) stay as they
// are, and the spawned worker records findings instead of moving the
// board.
const workerBoardRefusal = "todo: the supervisor owns the board; a worker does not claim, start, complete, fail, accept, or reject its entries (findings go in note and rem)"

func boardTransition(action string) bool {
	switch action {
	case "claim", "start", "complete", "fail", "accept", "reject":
		return true
	}
	return false
}

func (a adapter) dispatch(ctx context.Context, g given, p todostore.Project, session string) (string, error) {
	if bool(a.mode) && boardTransition(g.Action) {
		return "", errors.New(workerBoardRefusal)
	}
	switch g.Action {
	case "":
		return "", fmt.Errorf("todo: action required")
	case "create":
		items, err := itemsOf(g.Tasks)
		if err != nil {
			return "", err
		}
		return todostore.Create(ctx, a.db, p, items, session)
	case "bind":
		return todostore.Read(ctx, a.db, p, session)
	case "prune":
		return todostore.Prune(ctx, a.db, p, session)
	case "claim":
		return todostore.Claim(ctx, a.db, p, session, g.Status)
	case "note":
		if g.ID == "" {
			return "", fmt.Errorf("action 'note' requires id")
		}
		if g.Note == "" {
			return "", fmt.Errorf("action 'note' requires note text")
		}
		return todostore.Note(ctx, a.db, p, g.ID, g.Note, session)
	case "accept":
		if g.ID == "" {
			return "", fmt.Errorf("action 'accept' requires id")
		}
		return todostore.Accept(ctx, a.db, p, g.ID, session)
	case "reject":
		if g.ID == "" {
			return "", fmt.Errorf("action 'reject' requires id")
		}
		if g.Note == "" {
			return "", fmt.Errorf("action 'reject' requires a reason")
		}
		return todostore.Reject(ctx, a.db, p, g.ID, g.Note, session)
	case "start", "complete", "fail", "release", "retry":
		if g.ID == "" {
			return "", fmt.Errorf("action '%s' requires id", g.Action)
		}
		switch g.Action {
		case "start":
			return todostore.Start(ctx, a.db, p, g.ID, session, bool(a.mode))
		case "complete":
			return todostore.Complete(ctx, a.db, p, g.ID, session, bool(a.mode))
		case "fail":
			return todostore.Fail(ctx, a.db, p, g.ID, session, bool(a.mode))
		case "release":
			return todostore.Release(ctx, a.db, p, g.ID, session)
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

// resolve answers "whose queue is this", in one order everywhere: the
// project named on the call (which binds the session), else the session's
// binding, else the launch directory when it is a repo, else the shared
// bucket of a directory that is not one. Nothing is inferred from the
// files a call touches: a queue belongs to the plan, not to the last path
// read, so a session that dips into another repo keeps its queue where it
// put it.
type target struct {
	p       todostore.Project
	session string
	prev    todostore.Binding
	had     bool
	named   bool
	source  string
}

// commits is whether the resolution is a binding the session asked for:
// a named project. An unattributable session records nothing anywhere;
// a bare bind reports instead and never reaches here.
func (t target) commits() bool {
	return t.named && todostore.RealSession(t.session)
}

func (t target) note() string {
	if !t.commits() {
		return ""
	}
	switch {
	case !t.had:
		return "bound to " + t.p.Label
	case t.prev.Scope != t.p.Key:
		return "bound to " + t.p.Label + " (was " + t.prev.Label + ")"
	}
	return ""
}

func (a adapter) resolve(ctx context.Context, g given, session string) (target, error) {
	raw := g.Project
	named := raw != nil && *raw != ""
	p := todostore.Project{}
	source := ""
	if named {
		dir := paths.Expand(*raw)
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			return target{}, fmt.Errorf("todo: no such project directory: %s", dir)
		}
		p, source = todostore.ProjectOf(dir), srcProject
	} else {
		if todostore.RealSession(session) {
			b, ok, err := todostore.BindingOf(ctx, a.db, session)
			if err != nil {
				return target{}, err
			}
			if ok {
				return target{p: b.Project(), session: session, prev: b, had: true, source: srcBinding}, nil
			}
		}
		wd, err := os.Getwd()
		if err != nil {
			return target{}, fmt.Errorf("todo: no working directory: %v", err)
		}
		p = todostore.ProjectOf(wd)
		if p.OutsideRepo {
			source = srcHost
		} else {
			source = srcCwd
		}
	}
	t := target{p: p, session: session, named: named, source: source}
	if named && todostore.RealSession(session) {
		prev, had, err := todostore.BindingOf(ctx, a.db, session)
		if err != nil {
			return target{}, err
		}
		t.prev, t.had = prev, had
	}
	return t, nil
}

// report answers "whose queue am I in" without touching it: what a bare
// `todo project` line shows.
func (a adapter) report(ctx context.Context, session string) (string, error) {
	t, err := a.resolve(ctx, given{}, session)
	if err != nil {
		return "", err
	}
	p, source := t.p, t.source
	switch source {
	case srcBinding:
		return "queue: " + p.Label + " (bound)", nil
	case srcCwd:
		return "queue: " + p.Label + " (this directory's repo; not bound: name project)", nil
	default:
		return "queue: " + p.Label + " (this directory is not a repo; not bound: todo project <path>)", nil
	}
}

func isWrite(action string) bool {
	switch action {
	case "create", "claim", "start", "complete", "fail", "release", "retry", "move", "prune", "note", "accept", "reject":
		return true
	default:
		return false
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
