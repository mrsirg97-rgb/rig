package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

// toolCmd is the thin, shared adapter over one of the model's own tools
// (decision 8): parse the line into the tool's JSON args, call Exec with
// the session threaded (the loop's own threading), and return the reply
// verbatim — success or refusal. No parallel implementation, no
// re-voicing: the queue the model reads is the queue the user reads,
// and the tool's own refusals teach the protocol.
type toolCmd struct {
	name  string
	parse func(args string) (json.RawMessage, error)
}

func (t toolCmd) Name() string { return t.name }

func (t toolCmd) Description() string {
	return "over the same " + t.name + " tool the model gets: the line is parsed into the tool's args, the reply printed verbatim"
}

func (t toolCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	tool, ok := e.Tools[t.name]
	if !ok {
		return "", fmt.Errorf("%s: no %s tool (the root did not put it in Env.Tools)", t.name, t.name)
	}
	raw, err := t.parse(args)
	if err != nil {
		return "", err // the adapter's own shape refusal, naming the shape
	}
	if e.Session != nil {
		if s := e.Session(); s != nil {
			ctx = core.WithSession(ctx, s)
		}
	}
	out, err := tool.Exec(ctx, raw)
	if err != nil {
		return out, err // the tool's refusal, verbatim — the dispatch prints it
	}
	return out, nil
}

// todoArgs is the todo line's syntax (decision 8's table): token-shaped,
// the per-action shape enforced at the boundary. A bare `todo create`
// passes {"action":"create"} to the tool, whose own refusal teaches that
// the queue is replaced.
func todoArgs(args string) (json.RawMessage, error) {
	fields := strings.Fields(args)
	switch {
	case len(fields) == 0:
		return json.RawMessage(`{"action":""}`), nil // the tool's own 'action required' voice
	case fields[0] == "read" && len(fields) == 1:
		return json.RawMessage(`{"action":"read"}`), nil
	case fields[0] == "create":
		if len(fields) == 1 {
			return json.RawMessage(`{"action":"create"}`), nil
		}
		// the whole remainder: one task's text, the interior verbatim.
		text := strings.TrimSpace(args[len("create"):])
		return json.Marshal(map[string]any{
			"action": "create",
			"tasks":  []map[string]any{{"text": text}},
		})
	case (fields[0] == "start" || fields[0] == "complete" || fields[0] == "fail" || fields[0] == "retry") && len(fields) == 2:
		return json.Marshal(map[string]any{"action": fields[0], "id": fields[1]})
	case fields[0] == "move" && len(fields) == 3:
		pos, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("todo: %q: not a position (todo move <id> <pos>)", fields[2])
		}
		return json.Marshal(map[string]any{"action": "move", "id": fields[1], "pos": pos})
	}
	switch {
	case len(fields) == 0:
		return nil, errors.New("todo: usage: todo read|create <text…>|start|complete|fail|retry <id>|move <id> <pos>")
	case fields[0] == "read":
		return nil, errors.New("todo: read takes no args (todo read)")
	case fields[0] == "create":
		return nil, errors.New("todo: create needs text (todo create <text…>)")
	case fields[0] == "start" || fields[0] == "complete" || fields[0] == "fail" || fields[0] == "retry":
		return nil, fmt.Errorf("todo: %s takes an id (todo %s <id>)", fields[0], fields[0])
	case fields[0] == "move":
		return nil, errors.New("todo: move takes an id and a position (todo move <id> <pos>)")
	default:
		return nil, fmt.Errorf("todo: unknown action %q (todo read|create <text…>|start|complete|fail|retry <id>|move <id> <pos>)", fields[0])
	}
}

// schedulerArgs is the scheduler line's syntax (decision 8's table): the
// create tail is total — five vixie fields, or the one-word cron 'once'
// plus its ISO token; a create that fits neither is the adapter's own
// refusal. The store still validates the cron it gets: the adapter
// parses, the store teaches.
func schedulerArgs(args string) (json.RawMessage, error) {
	fields := strings.Fields(args)
	switch {
	case len(fields) == 0:
		return json.RawMessage(`{"action":""}`), nil // the tool's own 'unknown action' voice
	case fields[0] == "list" && len(fields) == 1:
		return json.RawMessage(`{"action":"list"}`), nil
	case (fields[0] == "pause" || fields[0] == "resume" || fields[0] == "remove") && len(fields) == 2:
		return json.Marshal(map[string]any{"action": fields[0], "id": fields[1]})
	case fields[0] == "runs" && (len(fields) == 2 || len(fields) == 3):
		m := map[string]any{"action": "runs", "id": fields[1]}
		if len(fields) == 3 {
			n, err := strconv.Atoi(fields[2])
			if err != nil {
				return nil, fmt.Errorf("scheduler: %q: not an integer (scheduler runs <id> [n])", fields[2])
			}
			m["n"] = n
		}
		return json.Marshal(m)
	case fields[0] == "create" && len(fields) >= 2:
		return schedulerCreate(fields)
	}
	switch {
	case len(fields) == 0:
		return nil, errors.New("scheduler: usage: scheduler list|create <name> <prompt…> <cron>|pause|resume|remove <id>|runs <id> [n]")
	case fields[0] == "list":
		return nil, errors.New("scheduler: list takes no args (scheduler list)")
	case fields[0] == "pause" || fields[0] == "resume" || fields[0] == "remove":
		return nil, fmt.Errorf("scheduler: %s takes an id (scheduler %s <id>)", fields[0], fields[0])
	case fields[0] == "runs":
		return nil, errors.New("scheduler: runs takes an id and an optional n (scheduler runs <id> [n])")
	default:
		return nil, fmt.Errorf("scheduler: unknown action %q (scheduler list|create <name> <prompt…> <cron>|pause|resume|remove <id>|runs <id> [n])", fields[0])
	}
}

// schedulerCreate splits the create line: the name is the first token
// after the action, the cron is the tail (five vixie fields, or 'once'
// plus its ISO token), and the prompt is the remainder between them.
func schedulerCreate(fields []string) (json.RawMessage, error) {
	const shape = "scheduler: create needs name, prompt, and a cron (5-field, or 'once' <ISO>)"
	rest := fields[2:]
	var (
		prompt []string
		cron   string
		at     string
	)
	switch {
	case len(rest) >= 3 && rest[len(rest)-2] == "once":
		prompt = rest[:len(rest)-2]
		cron, at = "once", rest[len(rest)-1]
	case len(rest) >= 6:
		prompt = rest[:len(rest)-5]
		cron = strings.Join(rest[len(rest)-5:], " ")
	default:
		return nil, errors.New(shape)
	}
	if len(prompt) == 0 {
		return nil, errors.New(shape)
	}
	m := map[string]any{
		"action": "create",
		"name":   fields[1],
		"prompt": strings.Join(prompt, " "),
		"cron":   cron,
	}
	if at != "" {
		m["at"] = at
	}
	return json.Marshal(m)
}
