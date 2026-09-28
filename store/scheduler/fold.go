package scheduler

import (
	"encoding/json"
	"fmt"
)

type jobState struct {
	ID          string
	Name        string
	Prompt      string
	Command     string
	Cron        string
	At          string
	Cwd         string
	Model       string
	Busy        string
	Timeout     int64
	TimeoutSet  bool
	Stall       int64
	StallSet    bool
	Budget      float64
	BudgetSet   bool
	State       string
	LastStatus  string
	LastTs      string
	LastExit    int64
	LastExitSet bool
	CreatedSeq  int64
	UpdatedSeq  int64
}

type fold struct {
	jobs       map[string]*jobState
	maxSeq     int64
	compactSeq int64
}

func newFold() *fold {
	return &fold{jobs: map[string]*jobState{}}
}

func (j *jobState) atPtr() *string {
	if j.At != "" {
		return &j.At
	}
	return nil
}
func (j *jobState) lastStatusPtr() *string {
	if j.LastStatus == "" {
		return nil
	}
	return &j.LastStatus
}
func (j *jobState) lastTsPtr() *string {
	if j.LastTs == "" {
		return nil
	}
	return &j.LastTs
}
func (j *jobState) lastExitPtr() *int64 {
	if !j.LastExitSet {
		return nil
	}
	return &j.LastExit
}
func (j *jobState) timeoutPtr() *int64 {
	if !j.TimeoutSet {
		return nil
	}
	return &j.Timeout
}
func (j *jobState) stallPtr() *int64 {
	if !j.StallSet {
		return nil
	}
	return &j.Stall
}
func (j *jobState) budgetPtr() *float64 {
	if !j.BudgetSet {
		return nil
	}
	return &j.Budget
}
func (j *jobState) commandPtr() *string {
	if j.Command == "" {
		return nil
	}
	return &j.Command
}

type eventRow struct {
	seq     int64
	op      string
	args    string
	session string
	ts      string
}

func (f *fold) mintID() string {
	max := 0
	for id := range f.jobs {
		if len(id) > 1 && id[0] == 'j' {
			n := 0
			for _, c := range id[1:] {
				if c < '0' || c > '9' {
					n = -1
					break
				}
				n = n*10 + int(c-'0')
			}
			if n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("j%d", max+1)
}

func (f *fold) apply(e eventRow) {
	if e.seq > f.maxSeq {
		f.maxSeq = e.seq
	}
	switch e.op {
	case "create":
		f.applyCreate(e)
	case "compact":
		f.applyCompact(e)
	default:
		f.applyVerb(e)
	}
}

func (f *fold) applyCreate(e eventRow) {
	var a struct {
		ID      string   `json:"id"`
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
	if json.Unmarshal([]byte(e.args), &a) != nil || a.Name == "" {
		return
	}

	id := a.ID
	if id != "" {

		if f.jobs[id] != nil {
			return
		}
	} else {
		for _, j := range f.jobs {
			if j.State != "removed" && j.Name == a.Name {
				return
			}
		}
		id = f.mintID()
	}
	var at string
	if a.At != nil {
		at = *a.At
	}
	f.jobs[id] = &jobState{
		ID: id, Name: a.Name, Prompt: a.Prompt, Command: a.Command,
		Cron: a.Cron, At: at, Cwd: a.Cwd, Model: a.Model,
		Busy: busyOf(a.Busy), State: "active",
		CreatedSeq: e.seq, UpdatedSeq: e.seq,
	}
	if a.Timeout != nil {
		f.jobs[id].Timeout = *a.Timeout
		f.jobs[id].TimeoutSet = true
	}
	if a.Stall != nil {
		f.jobs[id].Stall = *a.Stall
		f.jobs[id].StallSet = *a.Stall > 0
	}
	if a.Budget != nil {
		f.jobs[id].Budget = *a.Budget
		f.jobs[id].BudgetSet = *a.Budget > 0
	}
}
