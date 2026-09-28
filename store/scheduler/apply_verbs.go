package scheduler

import (
	"encoding/json"
)

func busyOf(b string) string {
	if b == "force" {
		return "force"
	}
	return "skip"
}

func (f *fold) applyVerb(e eventRow) {
	var a struct {
		ID     string   `json:"id"`
		Status string   `json:"status"`
		Exit   *float64 `json:"exit"`
		Reason string   `json:"reason"`
	}
	if json.Unmarshal([]byte(e.args), &a) != nil || a.ID == "" {
		return
	}
	j, ok := f.jobs[a.ID]
	if !ok {
		return
	}
	switch e.op {
	case "pause":
		if j.State != "active" {
			return
		}
		j.State = "paused"
	case "resume":
		if j.State != "paused" {
			return
		}
		j.State = "active"
	case "remove":
		if j.State == "removed" {
			return
		}
		j.State = "removed"
	case "done":
		if j.State == "removed" || j.State == "done" {
			return
		}
		j.State = "done"
	case "run":
		if a.Status != "" {
			j.LastStatus = a.Status
		}
		j.LastTs = e.ts
		if a.Exit != nil {
			j.LastExit = int64(*a.Exit)
			j.LastExitSet = true
		}
	case "update":
		j.applyUpdate(e.args)
	default:
		return
	}
	j.UpdatedSeq = e.seq
}

func (j *jobState) applyUpdate(args string) {
	var u struct {
		Name    string   `json:"name"`
		Prompt  string   `json:"prompt"`
		Command string   `json:"command"`
		Cron    string   `json:"cron"`
		At      *string  `json:"at"`
		Cwd     string   `json:"cwd"`
		Model   string   `json:"model"`
		Busy    string   `json:"busy"`
		Timeout *int64   `json:"timeout"`
		Stall   *int64   `json:"stall"`
		Budget  *float64 `json:"budget"`
	}
	if json.Unmarshal([]byte(args), &u) != nil {
		return
	}
	if u.Name != "" {
		j.Name = u.Name
	}
	if u.Prompt != "" {
		j.Prompt = u.Prompt
	}
	if u.Command != "" {
		j.Command = u.Command
	}
	if u.Cron != "" {
		j.Cron = u.Cron
		if u.At == nil {
			j.At = ""
		} else {
			j.At = *u.At
		}
	}
	if u.Cwd != "" {
		j.Cwd = u.Cwd
	}
	if u.Model != "" {
		j.Model = u.Model
	}
	if u.Busy != "" {
		j.Busy = busyOf(u.Busy)
	}
	if u.Timeout != nil {
		j.Timeout = *u.Timeout
		j.TimeoutSet = *u.Timeout > 0
	}
	if u.Stall != nil {
		j.Stall = *u.Stall
		j.StallSet = *u.Stall > 0
	}
	if u.Budget != nil {
		j.Budget = *u.Budget
		j.BudgetSet = *u.Budget > 0
	}
}
