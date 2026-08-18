// Package scheduler adapts the scheduler store to rig's tool seam: the
// description, guidelines, schema, and runtime voices; the Exec mapping
// onto the store's verbs with the threaded session attribution (the
// adapter consumes opened seams).
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

// description is the tool's description, built from the default job
// model (SPEC_CONFIG 5: the constant leaves code for the settings' file
// layer; the root passes the resolved value).
func description(defModel string) string {
	return "Background jobs on the user's crontab, bound to the worker GPU. " +
		"create schedules a headless worker session (5-field vixie cron 'M H D Mo DOW', " +
		"or cron:'once' + at:<ISO> for one-shot; the line self-deletes after one fire, no " +
		"retry). Jobs run under flock via the run-job verb, skip when another model holds the GPU " +
		"(busy:'skip' default; 'force' evicts), and log to the scheduler home (runs/). " +
		"list shows jobs in both scopes with drift between store and crontab. " +
		"pause/resume/remove manage jobs; runs gives the audit trail (n last, default 5). " +
		"Two stores: scope 'global' (this user) and 'cwd' (this working directory); ids jN " +
		"are minted per scope, never reused - copy them from list, never invent. " +
		"Default model: " + defModel + "."
}

// guidelines is the operational guidance; folded after the
// description in Description() since rig's tool surface carries no
// separate guidelines channel.
const guidelines = "Guidelines: " +
	"recurring/one-shot background work -> scheduler create; the job runs a headless worker session on the worker model. " +
	"default scope is cwd; scope:'global' for machine-wide jobs. ids jN are per scope: copy from list, never invent. " +
	"busy:'skip' (default) skips a fire while another model holds the GPU; 'force' evicts it - only when the user wants the GPU now. " +
	"cron is 5-field 'M H D Mo DOW'; 'once' + at:<ISO> fires at that minute and self-deletes; a failed once job is done-with-fail, re-create to retry. " +
	"list flags drift (store vs crontab): a drifting job is not trustworthy until the drift note is gone."

// schemaJSON is the tool's schema, built from the default job model
// (SPEC_CONFIG 5: the parameter's voice is the passed value).
func schemaJSON(defModel string) string {
	return `{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"enum": ["create", "list", "pause", "resume", "remove", "runs"]
		},
		"name": {
			"type": "string",
			"description": "Unique job name per scope. Required for create."
		},
		"prompt": {
			"type": "string",
			"description": "The prompt a worker pi session runs. Required for create."
		},
		"cron": {
			"type": "string",
			"description": "5-field vixie cron 'M H D Mo DOW' or 'once'. Required for create."
		},
		"at": {
			"type": "string",
			"description": "ISO time; required when cron is 'once'."
		},
		"scope": {
			"type": "string",
			"enum": ["global", "cwd"]
		},
		"model": {
			"type": "string",
			"description": "pi model id (default ` + defModel + `)."
		},
		"busy": {
			"type": "string",
			"enum": ["skip", "force"]
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
	Action string `json:"action"`
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
	Cron   string `json:"cron"`
	At     string `json:"at"`
	Scope  string `json:"scope"`
	Model  string `json:"model"`
	Busy   string `json:"busy"`
	Cwd    string `json:"cwd"`
	ID     string `json:"id"`
	N      *int   `json:"n"`
}

type adapter struct {
	st        sched.Stores
	ct        sched.Crontab
	runnerCmd string
	defModel  string // the default job model (the settings' defaultJobModel)
}

// New consumes the opened seams: both-scope stores, the crontab shim,
// the runner command the crontab lines embed, and the default job model
// (SPEC_CONFIG 5) — the description, the schema text, and the job a
// create without a model are all built from it.
func New(st sched.Stores, ct sched.Crontab, runnerCmd, defModel string) core.Tool {
	return adapter{st: st, ct: ct, runnerCmd: runnerCmd, defModel: defModel}
}

// Name implements core.Tool.
func (a adapter) Name() string { return "scheduler" }

// Description implements core.Tool: the description with guidelines
// folded (rig's surface has no separate channel).
func (a adapter) Description() string { return description(a.defModel) + "\n\n" + guidelines }

// Guidelines is the operational guidance alone, for composers that keep
// the channels separate.
func Guidelines() string { return guidelines }

// Schema implements core.Tool.
func (a adapter) Schema() json.RawMessage { return json.RawMessage(schemaJSON(a.defModel)) }

// Exec maps the call onto the store's verbs; the store's own refusals
// pass through. Session attribution from the threaded session, anon when
// unthreaded.
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
	// scope validates before any action voice: a
	// malformed scope is the answer even when the action would refuse.
	scope, err := scopeCheck(g.Scope)
	if err != nil {
		return "", err
	}

	switch g.Action {
	case "create":
		name := strings.TrimSpace(g.Name)
		if name == "" {
			return "", fmt.Errorf("scheduler: create requires 'name'")
		}
		if strings.TrimSpace(g.Prompt) == "" {
			return "", fmt.Errorf("scheduler: create requires 'prompt'")
		}
		if strings.TrimSpace(g.Cron) == "" {
			return "", fmt.Errorf("scheduler: create requires 'cron' (5-field or 'once' + 'at')")
		}
		model := a.defModel
		if g.Model != "" {
			model = g.Model
		}
		busy := "skip"
		if g.Busy == "force" {
			busy = "force"
		}
		return sched.Create(ctx, a.st, a.ct, sched.CreateInput{
			Name: name, Prompt: g.Prompt, Cron: g.Cron, At: g.At,
			Scope: scope, Model: model, Busy: busy, Cwd: g.Cwd,
		}, cwd, session, a.runnerCmd, time.Now)
	case "list":
		return sched.List(ctx, a.st, a.ct, cwd, nil, time.Now)
	case "pause", "resume", "remove":
		if g.ID == "" {
			return "", fmt.Errorf("scheduler: %s requires 'id' (jN)", g.Action)
		}
		switch g.Action {
		case "pause":
			return sched.Pause(ctx, a.st, a.ct, g.ID, scope, cwd, session)
		case "resume":
			return sched.Resume(ctx, a.st, a.ct, g.ID, scope, cwd, session)
		default:
			return sched.Remove(ctx, a.st, a.ct, g.ID, scope, cwd, session)
		}
	case "runs":
		if g.ID == "" {
			return "", fmt.Errorf("scheduler: runs requires 'id' (jN)")
		}
		n := 0
		if g.N != nil {
			n = *g.N
		}
		return sched.Runs(ctx, a.st, g.ID, scope, n)
	default:
		return "", fmt.Errorf("scheduler: unknown action '%s'", g.Action)
	}
}

// scopeCheck enforces the scope voice; empty passes through as the
// caller's default scope.
func scopeCheck(scope string) (string, error) {
	if scope == "" || scope == "global" || scope == "cwd" {
		return scope, nil
	}
	return "", fmt.Errorf("scheduler: scope must be 'global' or 'cwd', got '%s'", scope)
}
