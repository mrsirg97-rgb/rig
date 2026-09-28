package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type UpdateInput struct {
	ID      string
	Name    string
	Prompt  string
	Command string
	Cron    string
	At      string
	Cwd     string
	Model   string
	Busy    string
	Timeout int
	Stall   int
	Budget  float64
}

func Update(ctx context.Context, db DB, ct Crontab, in UpdateInput, session, runnerCmd, home string, now func() time.Time) (string, error) {
	if now == nil {
		now = time.Now
	}
	name := strings.TrimSpace(in.Name)
	prompt := in.Prompt
	command := strings.TrimSpace(in.Command)
	model := strings.TrimSpace(in.Model)
	cwd := strings.TrimSpace(in.Cwd)
	cron := strings.TrimSpace(in.Cron)
	at := in.At
	busy := in.Busy
	timeout := in.Timeout
	timeoutReset := timeout == -1
	if timeoutReset {
		timeout = 0
	}
	stall := in.Stall
	stallReset := stall == -1
	if stallReset {
		stall = 0
	}
	budget := in.Budget
	budgetReset := budget == -1
	if budgetReset {
		budget = 0
	}

	var newCron, newAt string
	switch {
	case cron != "" && at != "" && cron != "once":
		return "", schedErr("update got both a cron and an at (one cadence per update)")
	case cron == "once":
		if at == "" {
			return "", schedErr("cron 'once' requires 'at' (ISO time)")
		}
		norm, five, err := onceFields(at, now())
		if err != nil {
			return "", err
		}
		newCron, newAt = five, norm
	case cron != "":
		if _, err := ValidateCron(cron); err != nil {
			return "", err
		}
		newCron = cron
	case at != "":
		norm, five, err := onceFields(at, now())
		if err != nil {
			return "", err
		}
		newCron, newAt = five, norm
	}

	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx)
	if err != nil {
		return "", err
	}
	job, found := f.jobs[in.ID]
	if !found {
		return "", schedErr("no job '%s'", in.ID)
	}
	if job.State == "removed" {
		return "", schedErr("job '%s' is removed", in.ID)
	}
	if name == "" && prompt == "" && command == "" && cron == "" && at == "" && model == "" && cwd == "" && busy == "" && in.Timeout == 0 && in.Stall == 0 && in.Budget == 0 {
		return "", schedErr("update needs a change")
	}
	if budget != 0 && budget < 0 {
		return "", schedErr("budget must be >= 0 dollars, got %v", budget)
	}
	if busy != "" && busy != "skip" && busy != "force" {
		return "", schedErr("busy must be 'skip' or 'force', got '%s'", busy)
	}
	if timeout != 0 && (timeout < 1 || timeout > MaxTimeoutMinutes) {
		return "", timeoutErr(timeout)
	}
	if stall != 0 && (stall < 1 || stall > MaxStallMinutes) {
		return "", stallErr(stall)
	}
	jobCommand := strings.TrimSpace(job.Command)
	if command != "" && jobCommand == "" {
		return "", schedErr("job '%s' runs a model prompt; update takes prompt (remove + create to convert it to a command)", in.ID)
	}
	if prompt != "" && jobCommand != "" {
		return "", schedErr("job '%s' runs a command; update takes command (remove + create to convert it to a prompt)", in.ID)
	}
	if model != "" && jobCommand != "" {
		return "", schedErr("command jobs need no model (job '%s' runs a command)", in.ID)
	}
	if busy != "" && jobCommand != "" {
		return "", schedErr("command jobs need no busy policy (job '%s' runs a command)", in.ID)
	}
	if in.Budget != 0 && jobCommand != "" {
		return "", schedErr("command jobs need no budget (job '%s' runs a command)", in.ID)
	}
	if name != "" && name != job.Name {
		for _, j := range f.jobs {
			if j.State != "removed" && j.ID != in.ID && j.Name == name {
				return "", schedErr("a job named '%s' already exists (state: %s); remove it first", name, j.State)
			}
		}
	}
	cadenceChanged := (cron != "" || at != "") && (newCron != job.Cron || newAt != job.At)
	if cadenceChanged {
		text, err := ct.List()
		if err != nil {
			return "", err
		}
		next, _ := UpsertLine(text, in.ID, newCron, runnerCmd, home)
		if job.State == "paused" {
			next, _ = SetPaused(next, in.ID, true, home)
		}
		if err := ct.Install(next); err != nil {
			return "", err
		}
	}

	if err := maybeCompact(bound, tx, f, session); err != nil {
		return "", err
	}
	args := map[string]any{"id": in.ID}
	if name != "" {
		args["name"] = name
	}
	if prompt != "" {
		args["prompt"] = prompt
	}
	if command != "" {
		args["command"] = command
	}
	if model != "" {
		args["model"] = model
	}
	if cwd != "" {
		args["cwd"] = cwd
	}
	if busy != "" {
		args["busy"] = busy
	}
	if timeout != 0 {
		args["timeout"] = timeout
	} else if timeoutReset {
		args["timeout"] = 0
	}
	if stall != 0 {
		args["stall"] = stall
	} else if stallReset {
		args["stall"] = 0
	}
	if budget != 0 {
		args["budget"] = budget
	} else if budgetReset {
		args["budget"] = 0
	}
	if cadenceChanged {
		args["cron"] = newCron
		var atPtr *string
		if newAt != "" {
			atPtr = &newAt
		}
		args["at"] = atPtr
	}
	argsJSON, _ := json.Marshal(args)
	seq, err := appendEvent(bound, f.maxSeq+1, "update", string(argsJSON), session)
	if err != nil {
		return "", err
	}
	f.apply(eventRow{seq: seq, ts: nowRFC3339(), op: "update", args: string(argsJSON)})
	row := f.jobs[in.ID]
	if err := rewrite(tx, f); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	line := &TaggedLine{Key: in.ID, Cron: row.Cron, Paused: row.State == "paused"}
	lines := jobLines(row, line, false, now)
	return replyText(fmt.Sprintf("updated %s '%s'", row.ID, row.Name), lines), nil
}
