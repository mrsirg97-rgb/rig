package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/pathguard"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

func description(defModel string) string {
	return "background jobs on the user's crontab: each job is a headless worker session on the worker " +
		"model (default: " + defModel + "), running in its own cwd; or, when command is set, a plain " +
		"cron command in that cwd (no model, no GPU)."
}

const guidelines = "Guidelines: recurring or later work -> create (cron 'M H D Mo DOW', or once + at:<ISO>, which " +
	"self-deletes after one fire); list is one list, this directory first, the rest grouped by each job's cwd, with any drift between store and crontab; " +
	"pause/resume/remove; runs is the audit trail. Reply: the job row or the list; ids (jN) are minted — copy from list, never invent. busy:skip (default) skips a fire while another model holds " +
	"the GPU, force evicts it — only when the user wants the GPU now; a drifting job is not trustworthy " +
	"until the note clears; a failed once job is done — re-create it to retry. A job bounds each fire " +
	"with timeout (minutes, default 30, ceiling 24h; update with -1 resets it) and stall (minutes, the " +
	"silence window: a fire that writes nothing for longer is killed as hung; NULL/0 = ceiling only; " +
	"update with -1 resets it). budget is dollars, summed from the recorded run " +
	"costs (SPEC_HOSTED 5): a model fire at the cap records a skip naming the spend; " +
	"update with -1 resets the cap; command jobs take no budget. command jobs run a fixed " +
	"shell line instead of a worker session: no GPU, no busy policy, prompt/model/busy refused; for " +
	"deterministic scripts (pollers, digests, backups), never for anything needing judgment."

func schemaJSON(defModel string) string {
	return `{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"enum": ["create", "update", "list", "pause", "resume", "remove", "runs"]
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
			"description": "Shell line run by sh -c in the job's cwd instead of a worker session (no model, no GPU). Refuses prompt/model/busy in the same call."
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
			"description": "worker model id (default ` + defModel + `)."
		},
		"busy": {
			"type": "string",
			"enum": ["skip", "force"]
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
		"cwd": {
			"type": "string",
			"description": "Working directory the job runs in (default: this session's cwd)."
		},
		"id": {
			"type": "string",
			"description": "Job id jN (as shown by list). Required for pause/resume/remove/runs."
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
	Action  string  `json:"action"`
	Name    string  `json:"name"`
	Prompt  string  `json:"prompt"`
	Command string  `json:"command"`
	Cron    string  `json:"cron"`
	At      string  `json:"at"`
	Model   string  `json:"model"`
	Busy    string  `json:"busy"`
	Timeout int     `json:"timeout"`
	Stall   int     `json:"stall"`
	Budget  float64 `json:"budget"`
	Cwd     string  `json:"cwd"`
	ID      string  `json:"id"`
	N       *int    `json:"n"`
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
			model = a.defModel
			if g.Model != "" {
				model = g.Model
			}
			busy = "skip"
			if g.Busy == "force" {
				busy = "force"
			}
		} else if g.Model != "" || g.Busy != "" {
			return "", fmt.Errorf("scheduler: a command job takes no model and no busy policy")
		}
		jobCwd := g.Cwd
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
		}, cwd, session, a.runnerCmd, time.Now)
	case "update":
		if g.ID == "" {
			return "", fmt.Errorf("scheduler: update requires 'id' (jN)")
		}
		updateCwd := g.Cwd
		if updateCwd != "" {
			validated, err := pathguard.Within(updateCwd, cwd, a.home)
			if err != nil {
				return "", fmt.Errorf("scheduler: %w", err)
			}
			updateCwd = validated
		}
		return sched.Update(ctx, a.db, a.ct, sched.UpdateInput{
			ID: g.ID, Name: g.Name, Prompt: g.Prompt, Command: g.Command, Cron: g.Cron,
			At: g.At, Cwd: updateCwd, Model: g.Model, Busy: g.Busy, Timeout: g.Timeout, Stall: g.Stall, Budget: g.Budget,
		}, session, a.runnerCmd, time.Now)
	case "list":
		return sched.List(ctx, a.db, a.ct, cwd, nil, time.Now)
	case "pause", "resume", "remove":
		if g.ID == "" {
			return "", fmt.Errorf("scheduler: %s requires 'id' (jN)", g.Action)
		}
		switch g.Action {
		case "pause":
			return sched.Pause(ctx, a.db, a.ct, g.ID, cwd, session)
		case "resume":
			return sched.Resume(ctx, a.db, a.ct, g.ID, cwd, session)
		default:
			return sched.Remove(ctx, a.db, a.ct, g.ID, cwd, session)
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
	default:
		return "", fmt.Errorf("scheduler: unknown action '%s'", g.Action)
	}
}
