package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/pathguard"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

func description(defModel string) string {
	return "Background jobs on the operator's crontab. Each job is a headless worker session on a worker " +
		"model: omit it and the fire runs on whatever is resident (default " + defModel + " when nothing is), " +
		"running in its own workspace; a job with command runs that shell line instead, with no model and no GPU."
}

const guidelines = "Guidelines: create for work that recurs (cron 'M H D Mo DOW') or runs later (once with at, " +
	"which self-deletes after one fire). list shows every job, this workspace first, then the rest by " +
	"workspace, with any drift between the store and the crontab; a drifting job is not trustworthy until the " +
	"note clears, and repair re-derives its crontab line, one job by id or every drifting one with none. runs " +
	"is the audit trail. Job ids (jN) come from list: copy them, never invent them. busy:skip is the only " +
	"policy: a fire waits for a free slot up to its timeout, or skips naming the holder; eviction is the " +
	"operator's act (the fleet is the resident model). A failed once job is done; re-create it to retry. " +
	"Command jobs are for deterministic scripts (pollers, digests, backups), never for anything needing " +
	"judgment. timeout, stall and budget bound each fire; the fields say how. Reply: the job row or the list."

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
			"description": "Unique job name. Required for create."
		},
		"prompt": {
			"type": "string",
			"description": "The prompt the worker session runs. Required for create unless command is set."
		},
		"command": {
			"type": "string",
			"description": "Shell line run by sh -c in the job's workspace instead of a worker session (no model, no GPU). Refuses prompt/model/busy in the same call."
		},
		"cron": {
			"type": "string",
			"description": "5-field vixie cron 'M H D Mo DOW' or 'once'. Required for create."
		},
		"at": {
			"type": "string",
			"description": "ISO time; required when cron is 'once'."
		},
		"model": {
			"type": "string",
			"description": "model: the worker model id; omit to run on whatever is resident (default ` + defModel + ` when nothing is)."
		},
		"busy": {
			"type": "string",
			"enum": ["skip"]
		},
		"timeout": {
			"type": "integer",
			"minimum": -1,
			"maximum": 1440,
			"description": "Wall-clock cap per fire, in minutes (runner default 30, ceiling 1440); on update, -1 resets to the default. Omit to leave unchanged."
		},
		"stall": {
			"type": "integer",
			"minimum": -1,
			"maximum": 1440,
			"description": "Silence window per fire, in minutes: a fire whose worker writes nothing for longer is killed as hung. NULL/0 = ceiling only. On update, -1 resets to the default. Omit to leave unchanged."
		},
		"budget": {
			"type": "number",
			"minimum": -1,
			"description": "Dollar cap for the job's model fires, summed from the recorded run costs. On update, -1 resets the cap. Command jobs take no budget."
		},
		"workspace": {
			"type": "string",
			"description": "the workspace the job runs in (default: this session's workspace)."
		},
		"id": {
			"type": "string",
			"description": "Job id jN (as shown by list). Required for pause/resume/remove/runs; repair takes it or none (none repairs every drifting job)."
		},
		"n": {
			"type": "integer",
			"minimum": 1,
			"maximum": 100,
			"description": "How many runs to show for action='runs' (default 5)."
		}
	}
}`
}

type given struct {
	Action    string  `json:"action"`
	Name      string  `json:"name"`
	Prompt    string  `json:"prompt"`
	Command   string  `json:"command"`
	Cron      string  `json:"cron"`
	At        string  `json:"at"`
	Model     *string `json:"model"`
	Busy      string  `json:"busy"`
	Timeout   int     `json:"timeout"`
	Stall     int     `json:"stall"`
	Budget    float64 `json:"budget"`
	Workspace string  `json:"workspace"`
	ID        string  `json:"id"`
	N         *int    `json:"n"`
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

func (a adapter) Description() string { return description(a.defModel) + "\n\n" + guidelines }

func Guidelines() string { return guidelines }

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
		busy := ""
		if command == "" {
			if g.Model != nil {
				model = strings.TrimSpace(*g.Model)
			}
			busy = "skip"
		} else if (g.Model != nil && *g.Model != "") || g.Busy != "" {
			return "", fmt.Errorf("scheduler: a command job takes no model and no busy policy")
		}
		jobCwd := g.Workspace
		if jobCwd != "" {
			validated, err := pathguard.Within(jobCwd, cwd, a.home)
			if err != nil {
				return "", fmt.Errorf("scheduler: %w", err)
			}
			jobCwd = validated
		}
		return sched.Create(ctx, a.db, a.ct, sched.CreateInput{
			Name: name, Prompt: g.Prompt, Command: command, Cron: g.Cron, At: g.At,
			Model: model, Busy: busy, Cwd: jobCwd, Timeout: g.Timeout, Stall: g.Stall, Budget: g.Budget,
		}, cwd, session, a.runnerCmd, a.home, time.Now)
	case "update":
		if g.ID == "" {
			return "", fmt.Errorf("scheduler: update requires 'id' (jN)")
		}
		updateCwd := g.Workspace
		if updateCwd != "" {
			validated, err := pathguard.Within(updateCwd, cwd, a.home)
			if err != nil {
				return "", fmt.Errorf("scheduler: %w", err)
			}
			updateCwd = validated
		}
		updateModel, err := updateModelArg(args)
		if err != nil {
			return "", err
		}
		return sched.Update(ctx, a.db, a.ct, sched.UpdateInput{
			ID: g.ID, Name: g.Name, Prompt: g.Prompt, Command: g.Command, Cron: g.Cron,
			At: g.At, Cwd: updateCwd, Model: updateModel, Busy: g.Busy, Timeout: g.Timeout, Stall: g.Stall, Budget: g.Budget,
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
		n := 0
		if g.N != nil {
			n = *g.N
		}
		return sched.Runs(ctx, a.db, g.ID, n)
	case "repair":
		return sched.Repair(ctx, a.db, a.ct, g.ID, a.runnerCmd, a.home)
	default:
		return "", fmt.Errorf("scheduler: unknown action '%s'", g.Action)
	}
}
