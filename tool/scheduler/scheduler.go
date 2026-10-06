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

type Scheduler interface {
	tool.Definition
	Exec(ctx context.Context, args json.RawMessage) (string, error)

	Create(ctx context.Context, in CreateInput) (string, error)
	Update(ctx context.Context, id string, in UpdateInput) (string, error)
	List(ctx context.Context) (string, error)
	Show(ctx context.Context, id string) (string, error)
	Pause(ctx context.Context, id string) (string, error)
	Resume(ctx context.Context, id string) (string, error)
	Remove(ctx context.Context, id string) (string, error)
	Runs(ctx context.Context, id string, n int) (string, error)
	Repair(ctx context.Context, id string) (string, error)
}

// CreateInput is one job as create takes it: the schedule (a 5-field
// cron, or "once" with At), the work (a Prompt for a worker, or a
// Command line for no model), and the limits. A zero value leaves each
// field to the store's default.
type CreateInput struct {
	Name      string
	Prompt    string
	Command   string
	Cron      string
	At        string
	Workspace string
	Model     string
	Timeout   int
	Budget    float64
}

// UpdateInput is the same fields as they change; the job is named by id
// on the verb itself, as every other verb names its job.
type UpdateInput struct {
	Name      string
	Prompt    string
	Command   string
	Cron      string
	At        string
	Workspace string
	Model     *string
	Timeout   int
	Budget    float64
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

func New(db sched.DB, ct sched.Crontab, runnerCmd, defModel, home string) Scheduler {
	return adapter{Definition: tool.Fill(tool.Def("scheduler"), "{default_model}", defModel), db: db, ct: ct, runnerCmd: runnerCmd, home: home}
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
	switch g.Action {
	case "create":
		model := ""
		if g.Model != nil {
			model = *g.Model
		}
		return a.Create(ctx, CreateInput{
			Name: g.Name, Prompt: g.Prompt, Command: g.Command, Cron: g.Cron, At: g.At,
			Workspace: g.Workspace, Model: model, Timeout: g.Timeout, Budget: g.Budget,
		})
	case "update":
		model, err := updateModelArg(args)
		if err != nil {
			return "", err
		}
		return a.Update(ctx, g.ID, UpdateInput{
			Name: g.Name, Prompt: g.Prompt, Command: g.Command, Cron: g.Cron, At: g.At,
			Workspace: g.Workspace, Model: model, Timeout: g.Timeout, Budget: g.Budget,
		})
	case "list":
		return a.List(ctx)
	case "show":
		return a.Show(ctx, g.ID)
	case "pause":
		return a.Pause(ctx, g.ID)
	case "resume":
		return a.Resume(ctx, g.ID)
	case "remove":
		return a.Remove(ctx, g.ID)
	case "runs":
		n := 0
		if g.N != nil {
			n = *g.N
		}
		return a.Runs(ctx, g.ID, n)
	case "repair":
		return a.Repair(ctx, g.ID)
	default:
		return "", fmt.Errorf("scheduler: unknown action '%s'", g.Action)
	}
}

func callerOf(ctx context.Context) (string, string, error) {
	session := "anon"
	if s, ok := core.SessionFrom(ctx); ok && s != nil {
		session = s.ID
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("scheduler: %v", err)
	}
	return session, cwd, nil
}

func (a adapter) Create(ctx context.Context, in CreateInput) (string, error) {
	session, cwd, err := callerOf(ctx)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return "", fmt.Errorf("scheduler: create requires 'name'")
	}
	command := strings.TrimSpace(in.Command)
	if command == "" && strings.TrimSpace(in.Prompt) == "" {
		return "", fmt.Errorf("scheduler: create requires 'prompt' or 'command'")
	}
	if strings.TrimSpace(in.Cron) == "" {
		return "", fmt.Errorf("scheduler: create requires 'cron' (5-field or 'once' + 'at')")
	}
	model := in.Model
	if command == "" {
		model = strings.TrimSpace(model)
	} else if model != "" {
		return "", fmt.Errorf("scheduler: a command job takes no model and no busy policy")
	}
	jobCwd := in.Workspace
	if jobCwd != "" {
		validated, err := pathguard.Within(jobCwd, cwd, a.home)
		if err != nil {
			return "", fmt.Errorf("scheduler: %w", err)
		}
		jobCwd = validated
	}
	return sched.Create(ctx, a.db, a.ct, sched.CreateInput{
		Name: name, Prompt: in.Prompt, Command: command, Cron: in.Cron, At: in.At,
		Model: model, Cwd: jobCwd, Timeout: in.Timeout, Budget: in.Budget,
	}, cwd, session, a.runnerCmd, a.home, time.Now)
}

func (a adapter) Update(ctx context.Context, id string, in UpdateInput) (string, error) {
	session, cwd, err := callerOf(ctx)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("scheduler: update requires 'id' (jN)")
	}
	updateCwd := in.Workspace
	if updateCwd != "" {
		validated, err := pathguard.Within(updateCwd, cwd, a.home)
		if err != nil {
			return "", fmt.Errorf("scheduler: %w", err)
		}
		updateCwd = validated
	}
	return sched.Update(ctx, a.db, a.ct, sched.UpdateInput{
		ID: id, Name: in.Name, Prompt: in.Prompt, Command: in.Command, Cron: in.Cron,
		At: in.At, Cwd: updateCwd, Model: in.Model, Timeout: in.Timeout, Budget: in.Budget,
	}, session, a.runnerCmd, a.home, time.Now)
}

func (a adapter) List(ctx context.Context) (string, error) {
	_, cwd, err := callerOf(ctx)
	if err != nil {
		return "", err
	}
	return sched.List(ctx, a.db, a.ct, cwd, a.home, nil, time.Now)
}

func (a adapter) Show(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("scheduler: show requires 'id' (jN)")
	}
	return sched.Show(ctx, a.db, a.ct, id, a.home, nil, time.Now)
}

func (a adapter) Pause(ctx context.Context, id string) (string, error) {
	session, cwd, err := callerOf(ctx)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("scheduler: pause requires 'id' (jN)")
	}
	return sched.Pause(ctx, a.db, a.ct, id, cwd, session, a.home)
}

func (a adapter) Resume(ctx context.Context, id string) (string, error) {
	session, cwd, err := callerOf(ctx)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("scheduler: resume requires 'id' (jN)")
	}
	return sched.Resume(ctx, a.db, a.ct, id, cwd, session, a.home)
}

func (a adapter) Remove(ctx context.Context, id string) (string, error) {
	session, cwd, err := callerOf(ctx)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("scheduler: remove requires 'id' (jN)")
	}
	return sched.Remove(ctx, a.db, a.ct, id, cwd, session, a.home)
}

func (a adapter) Runs(ctx context.Context, id string, n int) (string, error) {
	if id == "" {
		return "", fmt.Errorf("scheduler: runs requires 'id' (jN)")
	}
	return sched.Runs(ctx, a.db, id, n)
}

func (a adapter) Repair(ctx context.Context, id string) (string, error) {
	return sched.Repair(ctx, a.db, a.ct, id, a.runnerCmd, a.home)
}
