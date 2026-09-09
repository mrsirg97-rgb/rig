package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

func description(defModel string) string {
	return "background jobs on the user's crontab: each job is a headless worker session on the worker " +
		"model (default: " + defModel + "), running in its own cwd."
}

const guidelines = "Guidelines: recurring or later work -> create (cron 'M H D Mo DOW', or once + at:<ISO>, which " +
	"self-deletes after one fire); list is one list, this directory first, the rest grouped by each job's cwd, with any drift between store and crontab; " +
	"pause/resume/remove; runs is the audit trail. Reply: the job row or the list; ids (jN) are minted — copy from list, never invent. busy:skip (default) skips a fire while another model holds " +
	"the GPU, force evicts it — only when the user wants the GPU now; a drifting job is not trustworthy " +
	"until the note clears; a failed once job is done — re-create it to retry."

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
			"description": "The prompt the worker session runs. Required for create."
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
	Model  string `json:"model"`
	Busy   string `json:"busy"`
	Cwd    string `json:"cwd"`
	ID     string `json:"id"`
	N      *int   `json:"n"`
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
		jobCwd := g.Cwd
		if jobCwd != "" {
			validated, err := validateJobCwd(jobCwd, cwd, a.home)
			if err != nil {
				return "", err
			}
			jobCwd = validated
		}
		return sched.Create(ctx, a.db, a.ct, sched.CreateInput{
			Name: name, Prompt: g.Prompt, Cron: g.Cron, At: g.At,
			Model: model, Busy: busy, Cwd: jobCwd,
		}, cwd, session, a.runnerCmd, time.Now)
	case "update":
		if g.ID == "" {
			return "", fmt.Errorf("scheduler: update requires 'id' (jN)")
		}
		updateCwd := g.Cwd
		if updateCwd != "" {
			validated, err := validateJobCwd(updateCwd, cwd, a.home)
			if err != nil {
				return "", err
			}
			updateCwd = validated
		}
		return sched.Update(ctx, a.db, a.ct, sched.UpdateInput{
			ID: g.ID, Name: g.Name, Prompt: g.Prompt, Cron: g.Cron,
			At: g.At, Cwd: updateCwd, Model: g.Model, Busy: g.Busy,
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

// validateJobCwd canonicalizes a job's working directory and refuses one
// that is not the caller's project or the rig home. The jail rw-binds the
// cwd (store/scheduler/jail.go, "--bind p.Cwd p.Cwd"), so a job must not be
// scoped to the rest of the host: an arbitrary absolute cwd (a home
// directory, /etc, /) would hand the jailed worker rw access to it. Same
// rule as the delegate tool's canonicalCwd: lexical and canonical
// (symlinks resolved), and the directory must exist (the jail binds it).
func validateJobCwd(path, sessionCwd, rigHome string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("scheduler: cwd %q: %v", path, err)
	}
	under := func(root string) bool {
		if root == "" {
			return false
		}
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			return false
		}
		rel, err := filepath.Rel(rootAbs, abs)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if !under(sessionCwd) && !under(rigHome) {
		return "", fmt.Errorf("scheduler: cwd %q is outside the session's cwd (%s) and the rig home (%s)", abs, sessionCwd, rigHome)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("scheduler: cwd %q: %v", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("scheduler: cwd %q is not a directory", abs)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("scheduler: cwd %q: %v", abs, err)
	}
	resolved = filepath.Clean(resolved)
	canonUnder := func(root string) bool {
		if root == "" {
			return false
		}
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return false
		}
		rel, err := filepath.Rel(filepath.Clean(resolvedRoot), resolved)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if !canonUnder(sessionCwd) && !canonUnder(rigHome) {
		return "", fmt.Errorf("scheduler: cwd %q is outside the session's cwd (%s) and the rig home (%s)", resolved, sessionCwd, rigHome)
	}
	return resolved, nil
}
