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
	"github.com/mrsirg97-rgb/rig/v2/tool"
)

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

	Workspace string `json:"workspace"`
	N         *int   `json:"n"`
}

type adapter struct {
	tool.Definition
	db        sched.DB
	ct        sched.Crontab
	runnerCmd string
	home      string
}

func New(db sched.DB, ct sched.Crontab, runnerCmd, defModel, home string) core.Tool {
	return adapter{Definition: tool.Def("scheduler").Fill("{default_model}", defModel), db: db, ct: ct, runnerCmd: runnerCmd, home: home}
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
			Model: model, Cwd: jobCwd, Timeout: g.Timeout, Budget: g.Budget,
		}, cwd, session, a.runnerCmd, a.home, time.Now)
	case "update":
		if g.ID == "" {
			return "", fmt.Errorf("scheduler: update requires 'id' (jN)")
		}
		updateModel, err := updateModelArg(args)
		if err != nil {
			return "", err
		}
		updateCwd := g.Workspace
		if updateCwd != "" {
			validated, err := pathguard.Within(updateCwd, cwd, a.home)
			if err != nil {
				return "", fmt.Errorf("scheduler: %w", err)
			}
			updateCwd = validated
		}
		return sched.Update(ctx, a.db, a.ct, sched.UpdateInput{
			ID: g.ID, Name: g.Name, Prompt: g.Prompt, Command: g.Command, Cron: g.Cron,
			At: g.At, Cwd: updateCwd, Model: updateModel, Timeout: g.Timeout, Budget: g.Budget,
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
