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

type toolCmd struct {
	name  string
	parse func(args string) (json.RawMessage, error)
}

func (t toolCmd) Name() string { return t.name }

func (t toolCmd) Description() string {
	switch t.name {
	case "todo":
		return "the task queue: read it, add a task, or move one (start, done, fail, retry)"
	case "scheduler":
		return "the cron jobs: list, create, update, pause, resume, remove, or show a job's runs"
	}
	return "over the same " + t.name + " tool the model gets: the line is parsed into the tool's args, the reply printed verbatim"
}

func (t toolCmd) Sub() []Sub {
	switch t.name {
	case "todo":
		return []Sub{
			{Name: "read", Desc: "show the queue"},
			{Name: "create", Desc: "add a task: create <text>"},
			{Name: "start", Desc: "mark a task in progress: start <id>"},
			{Name: "done", Desc: "mark a task complete: done <id>"},
			{Name: "fail", Desc: "mark a task failed: fail <id>"},
			{Name: "retry", Desc: "put a failed task back: retry <id>"},
			{Name: "project", Desc: "show another project's queue: project <path>"},
		}
	case "scheduler":
		return []Sub{
			{Name: "list", Desc: "show the jobs"},
			{Name: "create", Desc: "add a job: create <name> <prompt…> <cron>"},
			{Name: "update", Desc: "change a job's fields: update <id> [name <n>] [model <m>] [cwd <dir>] [busy <skip|force>] [cron <5 fields|once>] [at <ISO>] [prompt <the rest of the line>]"},
			{Name: "runs", Desc: "show a job's runs: runs <id> [n]"},
			{Name: "pause", Desc: "pause a job: pause <id>"},
			{Name: "resume", Desc: "resume a paused job: resume <id>"},
			{Name: "remove", Desc: "remove a job: remove <id>"},
		}
	}
	return nil
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
		return "", err
	}
	if e.Session != nil {
		if s := e.Session(); s != nil {
			ctx = core.WithSession(ctx, s)
		}
	}
	out, err := tool.Exec(ctx, raw)
	if err != nil {
		return out, err
	}
	return out, nil
}

func todoArgs(args string) (json.RawMessage, error) {
	fields := strings.Fields(args)
	switch {
	case len(fields) == 0:
		return json.RawMessage(`{"action":""}`), nil
	case fields[0] == "read" && len(fields) == 1:
		return json.RawMessage(`{"action":"read"}`), nil
	case fields[0] == "create":
		if len(fields) == 1 {
			return json.RawMessage(`{"action":"create"}`), nil
		}
		text := strings.TrimSpace(args[len("create"):])
		return json.Marshal(map[string]any{
			"action": "create",
			"tasks":  []map[string]any{{"text": text}},
		})
	case (fields[0] == "start" || fields[0] == "complete" || fields[0] == "done" || fields[0] == "fail" || fields[0] == "retry") && len(fields) == 2:
		action := fields[0]
		if action == "done" {
			action = "complete"
		}
		return json.Marshal(map[string]any{"action": action, "id": fields[1]})
	case fields[0] == "move" && len(fields) == 3:
		pos, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("todo: %q: not a position (todo move <id> <pos>)", fields[2])
		}
		return json.Marshal(map[string]any{"action": "move", "id": fields[1], "pos": pos})
	case fields[0] == "project" && len(fields) == 2:
		return json.Marshal(map[string]any{"action": "read", "project": fields[1]})
	}
	switch {
	case len(fields) == 0:
		return nil, errors.New("todo: usage: todo read|create <text…>|start|complete|fail|retry <id>|move <id> <pos>|project <path>")
	case fields[0] == "read":
		return nil, errors.New("todo: read takes no args (todo read)")
	case fields[0] == "create":
		return nil, errors.New("todo: create needs text (todo create <text…>)")
	case fields[0] == "start" || fields[0] == "complete" || fields[0] == "done" || fields[0] == "fail" || fields[0] == "retry":
		return nil, fmt.Errorf("todo: %s takes an id (todo %s <id>)", fields[0], fields[0])
	case fields[0] == "move":
		return nil, errors.New("todo: move takes an id and a position (todo move <id> <pos>)")
	case fields[0] == "project":
		return nil, errors.New("todo: project takes a path (todo project <path>)")
	default:
		return nil, fmt.Errorf("todo: unknown action %q (todo read|create <text…>|start|complete|fail|retry <id>|move <id> <pos>)", fields[0])
	}
}

const schedulerVerbs = "list|create <name> <prompt…> <cron>|update <id> [name <n>] [model <m>] [cwd <dir>] [busy <skip|force>] [cron <5 fields|once>] [at <ISO>] [prompt <the rest of the line>]|pause|resume|remove <id>|runs <id> [n]"

func schedulerArgs(args string) (json.RawMessage, error) {
	fields := strings.Fields(args)
	switch {
	case len(fields) == 0:
		return json.RawMessage(`{"action":""}`), nil
	case fields[0] == "list" && len(fields) == 1:
		return json.RawMessage(`{"action":"list"}`), nil
	case fields[0] == "update" && len(fields) >= 2:
		return schedulerUpdate(fields)
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
		return nil, errors.New("scheduler: usage: scheduler " + schedulerVerbs)
	case fields[0] == "list":
		return nil, errors.New("scheduler: list takes no args (scheduler list)")
	case fields[0] == "update":
		return nil, errors.New("scheduler: update takes an id and named fields (scheduler " + schedulerVerbs)
	case fields[0] == "pause" || fields[0] == "resume" || fields[0] == "remove":
		return nil, fmt.Errorf("scheduler: %s takes an id (scheduler %s <id>)", fields[0], fields[0])
	case fields[0] == "runs":
		return nil, errors.New("scheduler: runs takes an id and an optional n (scheduler runs <id> [n])")
	default:
		return nil, fmt.Errorf("scheduler: unknown action %q (scheduler %s)", fields[0], schedulerVerbs)
	}
}

func isUpdateKey(s string) bool {
	switch s {
	case "name", "prompt", "cron", "at", "model", "cwd", "busy":
		return true
	}
	return false
}

func schedulerUpdate(fields []string) (json.RawMessage, error) {
	const keys = "name, prompt, cron, at, model, cwd, busy"
	const shape = "scheduler update <id> [name <n>] [model <m>] [cwd <dir>] [busy <skip|force>] [cron <5 fields|once>] [at <ISO>] [prompt <the rest of the line>]"
	m := map[string]any{"action": "update", "id": fields[1]}
	rest := fields[2:]
	i := 0
	for i < len(rest) {
		key := rest[i]
		i++
		switch key {
		case "prompt":
			if i >= len(rest) {
				return nil, fmt.Errorf("scheduler: update: %q needs a value (%s)", key, shape)
			}
			m[key] = strings.Join(rest[i:], " ")
			i = len(rest)
		case "cron":
			want := 5
			if i < len(rest) && rest[i] == "once" {
				want = 1
			}
			if i+want > len(rest) {
				return nil, fmt.Errorf("scheduler: update: %q needs five fields or once (%s)", key, shape)
			}
			m[key] = strings.Join(rest[i:i+want], " ")
			i += want
		case "name", "at", "model", "cwd", "busy":
			if i >= len(rest) {
				return nil, fmt.Errorf("scheduler: update: %q needs a value (%s)", key, shape)
			}
			m[key] = rest[i]
			i++
		default:
			return nil, fmt.Errorf("scheduler: update: unknown key %q (keys: %s)", key, keys)
		}
	}
	return json.Marshal(m)
}

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
