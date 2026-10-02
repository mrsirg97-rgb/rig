package todo

import (
	"context"
	"errors"
	"fmt"

	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

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
		reply, err := todostore.Create(ctx, a.db, p, items, session)
		if err != nil {
			return reply, err
		}
		a.wakeRouter()
		return reply, nil
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
	case "notes":
		if g.ID == "" {
			return "", fmt.Errorf("action 'notes' requires id")
		}
		return todostore.Notes(ctx, a.db, p, g.ID, session)
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
			reply, err := todostore.Complete(ctx, a.db, p, g.ID, session, bool(a.mode))
			if err != nil {
				return reply, err
			}
			a.wakeRouter()
			return reply, nil
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
		if g.ID != "" {
			return todostore.ReadOne(ctx, a.db, p, g.ID, session)
		}
		if g.All != nil && *g.All {
			return todostore.ReadAll(ctx, a.db, p, session)
		}
		return todostore.Read(ctx, a.db, p, session)
	case "finished":
		n := 0
		if g.N != nil {
			n = *g.N
		}
		return todostore.ReadFinished(ctx, a.db, p, session, n)
	default:
		return "", fmt.Errorf("todo: unknown action %q", g.Action)
	}
}
