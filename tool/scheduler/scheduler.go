package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

func description(defModel string) string {
	return "Background jobs on the operator's crontab: each is a headless worker session on a worker " +
		"model: omit it and the fire runs on whatever is resident (default " + defModel + " when nothing is), " +
		"running in its own workspace; a command job runs a shell line with no model and no GPU.\n\nGuidelines: create for " +
		"work that recurs (cron 'M H D Mo DOW') or once (at, self-deletes after one fire); command jobs are " +
		"for deterministic scripts, never " +
		"judgment calls. list shows every job with any store-vs-crontab drift — a drifting job is untrustworthy " +
		"until the note clears; repair re-derives its crontab line, one job by id or every drifting one. " +
		"runs is the audit trail; job ids (jN) come from list: copy them, never invent them. the fire sends and " +
		"waits on the " +
		"queue; a different resident model skips, naming the holder; eviction is the operator's act (the fleet " +
		"is the resident model). " +
		"A failed once job is done; re-create it to retry. timeout and budget bound each fire. Reply: the job " +
		"row or the list."
}

func schemaJSON(defModel string) string {
	return `{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"enum": ["create", "update", "list", "pause", "resume", "remove", "runs", "repair"]
		},
		"name": {
			"type": "string",
			"description": "Unique job name; required."
		},
		"prompt": {
			"type": "string",
			"description": "The prompt the worker session runs; required unless command is set."
		},
		"command": {
			"type": "string",
			"description": "Shell line for sh -c in the job's workspace; refuses prompt/model."
		},
		"cron": {
			"type": "string",
			"description": "5-field vixie cron 'M H D Mo DOW' or 'once'; required."
		},
		"at": {
			"type": "string",
			"description": "ISO time."
		},
		"model": {
			"type": "string",
			"description": "model: the worker model id; omit to run on whatever is resident (default ` + defModel + ` when nothing is)."
		},
		"timeout": {
			"type": "integer",
			"minimum": -1,
			"maximum": 1440,
			"description": "Wall-clock cap per fire, minutes (default 30, ceiling 1440); -1 resets."
		},
		"budget": {
			"type": "number",
			"minimum": -1,
			"description": "Dollar cap on the job's model fires, from the run costs; -1 resets; command jobs take none."
		},
		"id": {
			"type": "string",
			"description": "Job id jN from list; required for pause/resume/remove/runs; repair takes it or none."
		}
	},
	"required": ["action"]
}`
}

type given struct {
	Action  string  `json:"action"`
	Name    string  `json:"name"`
	Prompt  string  `json:"prompt"`
	Command string  `json:"command"`
	Cron    string  `json:"cron"`
	At      string  `json:"at"`
	Model   *string `json:"model"`

	Timeout int `json:"timeout"`

	Budget float64 `json:"budget"`
	ID     string  `json:"id"`
}

type adapter struct {
	db        sched.DB
	ct        sched.Crontab
	runnerCmd string
	defModel  string
	home      string
}

func New(db sched.DB, ct sched.Crontab, runnerCmd, defModel, home string) core.Tool {
	return adapter{db: db, ct: ct, runnerCmd: runnerCmd, defModel: defModel, home: home}
}

func updateModelArg(args json.RawMessage) (*string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return nil, fmt.Errorf("scheduler: %v", err)
	}
	raw, ok := fields["model"]
	if !ok {
		return nil, nil
	}
	if string(raw) == "null" {
		return new(string), nil
	}
	var m string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("scheduler: model must be a string or null: %v", err)
	}
	return &m, nil
}

func (a adapter) Name() string { return "scheduler" }

func (a adapter) Description() string { return description(a.defModel) }

func (a adapter) Schema() json.RawMessage { return json.RawMessage(schemaJSON(a.defModel)) }

func (a adapter) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var g given
	if err := json.Unmarshal(args, &g); err != nil {
		return "", fmt.Errorf("scheduler: %v", err)
	}
	session := "anon"
	if s, ok := core.SessionFrom(ctx); ok && s != nil {
		session = s.ID
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("scheduler: %v", err)
	}

	switch g.Action {
	case "create":
		name := strings.TrimSpace(g.Name)
		if name == "" {
			return "", fmt.Errorf("scheduler: create requires 'name'")
		}
		command := strings.TrimSpace(g.Command)
		if command == "" && strings.TrimSpace(g.Prompt) == "" {
			return "", fmt.Errorf("scheduler: create requires 'prompt' or 'command'")
		}
		if strings.TrimSpace(g.Cron) == "" {
			return "", fmt.Errorf("scheduler: create requires 'cron' (5-field or 'once' + 'at')")
		}
		model := ""
		if command == "" {
			if g.Model != nil {
				model = strings.TrimSpace(*g.Model)
			}
		} else if g.Model != nil && *g.Model != "" {
			return "", fmt.Errorf("scheduler: a command job takes no model and no busy policy")
		}
		return sched.Create(ctx, a.db, a.ct, sched.CreateInput{
			Name: name, Prompt: g.Prompt, Command: command, Cron: g.Cron, At: g.At,
			Model: model, Cwd: "", Timeout: g.Timeout, Budget: g.Budget,
		}, cwd, session, a.runnerCmd, a.home, time.Now)
	case "update":
		if g.ID == "" {
			return "", fmt.Errorf("scheduler: update requires 'id' (jN)")
		}
		updateModel, err := updateModelArg(args)
		if err != nil {
			return "", err
		}
		return sched.Update(ctx, a.db, a.ct, sched.UpdateInput{
			ID: g.ID, Name: g.Name, Prompt: g.Prompt, Command: g.Command, Cron: g.Cron,
			At: g.At, Cwd: "", Model: updateModel, Timeout: g.Timeout, Budget: g.Budget,
		}, session, a.runnerCmd, a.home, time.Now)
	case "list":
		return sched.List(ctx, a.db, a.ct, cwd, a.home, nil, time.Now)
	case "pause", "resume", "remove":
		if g.ID == "" {
			return "", fmt.Errorf("scheduler: %s requires 'id' (jN)", g.Action)
		}
		switch g.Action {
		case "pause":
			return sched.Pause(ctx, a.db, a.ct, g.ID, cwd, session, a.home)
		case "resume":
			return sched.Resume(ctx, a.db, a.ct, g.ID, cwd, session, a.home)
		default:
			return sched.Remove(ctx, a.db, a.ct, g.ID, cwd, session, a.home)
		}
	case "runs":
		if g.ID == "" {
			return "", fmt.Errorf("scheduler: runs requires 'id' (jN)")
		}
		return sched.Runs(ctx, a.db, g.ID, 0)
	case "repair":
		return sched.Repair(ctx, a.db, a.ct, g.ID, a.runnerCmd, a.home)
	default:
		return "", fmt.Errorf("scheduler: unknown action '%s'", g.Action)
	}
}
