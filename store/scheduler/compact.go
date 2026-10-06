package scheduler

import (
	"encoding/json"
)

type compactJob struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Prompt     string   `json:"prompt"`
	Command    *string  `json:"command"`
	Cron       string   `json:"cron"`
	At         *string  `json:"at"`
	Cwd        string   `json:"cwd"`
	Model      string   `json:"model"`
	Busy       string   `json:"busy"`
	Timeout    *int64   `json:"timeout"`
	Stall      *int64   `json:"stall"`
	Budget     *float64 `json:"budget"`
	State      string   `json:"state"`
	CreatedSeq int64    `json:"created_seq"`
	UpdatedSeq int64    `json:"updated_seq"`
	LastStatus *string  `json:"lastStatus"`
	LastTs     *string  `json:"lastTs"`
	LastExit   *int64   `json:"lastExit"`
}

func (f *fold) applyCompact(e eventRow) {
	var a struct {
		Jobs []compactJob `json:"jobs"`
	}
	tasks := map[string]*jobState{}
	if json.Unmarshal([]byte(e.args), &a) == nil {
		for _, r := range a.Jobs {
			if r.ID == "" || r.Name == "" {
				continue
			}
			state := r.State
			switch state {
			case "active", "paused", "done", "removed":
			default:
				continue
			}
			j := &jobState{
				ID: r.ID, Name: r.Name, Prompt: r.Prompt, Cron: r.Cron,
				Cwd: r.Cwd, Model: r.Model, Busy: busyOf(r.Busy),
				State: state,
			}
			if r.Command != nil {
				j.Command = *r.Command
			}
			if r.Timeout != nil {
				j.Timeout = *r.Timeout
				j.TimeoutSet = *r.Timeout > 0
			}
			if r.Stall != nil {
				j.Stall = *r.Stall
				j.StallSet = *r.Stall > 0
			}
			if r.Budget != nil {
				j.Budget = *r.Budget
				j.BudgetSet = *r.Budget > 0
			}
			if r.At != nil {
				j.At = *r.At
			}
			if r.LastStatus != nil {
				j.LastStatus = *r.LastStatus
			}
			if r.LastTs != nil {
				j.LastTs = *r.LastTs
			}
			if r.LastExit != nil {
				j.LastExit = *r.LastExit
				j.LastExitSet = true
			}
			j.consumeFiredOnce()
			tasks[r.ID] = j
		}
	}
	for _, j := range tasks {
		j.CreatedSeq = e.seq
		j.UpdatedSeq = e.seq
	}
	f.jobs = tasks
	f.compactSeq = e.seq
}
