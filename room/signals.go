package room

import (
	"encoding/json"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
)

type StartChild interface {
	evt.Signal
	startChild()
	Model() string
	Port() int
	Command() string
	Env() []string
	Log() string
}

type startChild struct {
	evt.Mark
	model   string
	port    int
	command string
	env     []string
	log     string
}

type startChildWire struct {
	Model   string
	Port    int
	Command string
	Env     []string
	Log     string
}

func NewStartChild(model string, port int, command string, env []string, log string) StartChild {
	return startChildOf(startChildWire{Model: model, Port: port, Command: command, Env: env, Log: log})
}

func startChildOf(w startChildWire) *startChild {
	return &startChild{model: w.Model, port: w.Port, command: w.Command, env: w.Env, log: w.Log}
}

func (s *startChild) startChild()     {}
func (s *startChild) Model() string   { return s.model }
func (s *startChild) Port() int       { return s.port }
func (s *startChild) Command() string { return s.command }
func (s *startChild) Env() []string   { return s.env }
func (s *startChild) Log() string     { return s.log }

func encodeStartChild(s *startChild) ([]byte, error) {
	return json.Marshal(startChildWire{Model: s.model, Port: s.port, Command: s.command, Env: s.env, Log: s.log})
}

func decodeStartChild(raw []byte) (*startChild, error) {
	var w startChildWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	return startChildOf(w), nil
}

type StopChild interface {
	evt.Signal
	stopChild()
	Model() string
	Grace() int
}

type stopChild struct {
	evt.Mark
	model string
	grace int
}

type stopChildWire struct {
	Model string
	Grace int
}

func NewStopChild(model string, grace int) StopChild {
	return stopChildOf(stopChildWire{Model: model, Grace: grace})
}

func stopChildOf(w stopChildWire) *stopChild {
	return &stopChild{model: w.Model, grace: w.Grace}
}

func (s *stopChild) stopChild()    {}
func (s *stopChild) Model() string { return s.model }
func (s *stopChild) Grace() int    { return s.grace }

func encodeStopChild(s *stopChild) ([]byte, error) {
	return json.Marshal(stopChildWire{Model: s.model, Grace: s.grace})
}

func decodeStopChild(raw []byte) (*stopChild, error) {
	var w stopChildWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	return stopChildOf(w), nil
}

type SwapChildInput struct {
	Model   string
	Out     string
	Port    int
	Command string
	Env     []string
	Log     string
	Grace   int
	Forced  bool
}

type SwapChild interface {
	evt.Signal
	swapChild()
	Model() string
	Out() string
	Port() int
	Command() string
	Env() []string
	Log() string
	Grace() int
	Forced() bool
	Swap() bool
}

type swapChild struct {
	evt.Mark
	model   string
	out     string
	port    int
	command string
	env     []string
	log     string
	grace   int
	forced  bool
}

type swapChildWire struct {
	Model   string
	Out     string
	Port    int
	Command string
	Env     []string
	Log     string
	Grace   int
	Forced  bool
}

func NewSwapChild(in SwapChildInput) SwapChild {
	return swapChildOf(swapChildWire{
		Model: in.Model, Out: in.Out, Port: in.Port, Command: in.Command,
		Env: in.Env, Log: in.Log, Grace: in.Grace, Forced: in.Forced,
	})
}

func swapChildOf(w swapChildWire) *swapChild {
	return &swapChild{
		model: w.Model, out: w.Out, port: w.Port, command: w.Command,
		env: w.Env, log: w.Log, grace: w.Grace, forced: w.Forced,
	}
}

func (s *swapChild) swapChild()      {}
func (s *swapChild) Model() string   { return s.model }
func (s *swapChild) Out() string     { return s.out }
func (s *swapChild) Port() int       { return s.port }
func (s *swapChild) Command() string { return s.command }
func (s *swapChild) Env() []string   { return s.env }
func (s *swapChild) Log() string     { return s.log }
func (s *swapChild) Grace() int      { return s.grace }
func (s *swapChild) Forced() bool    { return s.forced }
func (s *swapChild) Swap() bool      { return true }

func encodeSwapChild(s *swapChild) ([]byte, error) {
	return json.Marshal(swapChildWire{
		Model: s.model, Out: s.out, Port: s.port, Command: s.command,
		Env: s.env, Log: s.log, Grace: s.grace, Forced: s.forced,
	})
}

func decodeSwapChild(raw []byte) (*swapChild, error) {
	var w swapChildWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	return swapChildOf(w), nil
}

type TableChange interface {
	evt.Signal
	tableChange()
	Row() json.RawMessage
}

type tableChange struct {
	evt.Mark
	row json.RawMessage
}

type tableChangeWire struct {
	Row json.RawMessage
}

func NewTableChange(row json.RawMessage) TableChange {
	return tableChangeOf(tableChangeWire{Row: row})
}

func tableChangeOf(w tableChangeWire) *tableChange {
	return &tableChange{row: w.Row}
}

func (t *tableChange) tableChange()         {}
func (t *tableChange) Row() json.RawMessage { return t.row }

func encodeTableChange(t *tableChange) ([]byte, error) {
	return json.Marshal(tableChangeWire{Row: t.row})
}

func decodeTableChange(raw []byte) (*tableChange, error) {
	var w tableChangeWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	return tableChangeOf(w), nil
}

type Update interface {
	evt.Signal
	update()
	Version() string
}

type update struct {
	evt.Mark
	version string
}

type updateWire struct {
	Version string
}

func NewUpdate(version string) Update {
	return updateOf(updateWire{Version: version})
}

func updateOf(w updateWire) *update {
	return &update{version: w.Version}
}

func (u *update) update()         {}
func (u *update) Version() string { return u.version }

func encodeUpdate(u *update) ([]byte, error) {
	return json.Marshal(updateWire{Version: u.version})
}

func decodeUpdate(raw []byte) (*update, error) {
	var w updateWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	return updateOf(w), nil
}

const (
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"

	JobStatusSince = "3.2.0"
)

const MemberLeftNotice = "member left"

type JobStatusInput struct {
	Job     string
	Prompt  string
	State   string
	Started time.Time
	Ended   time.Time
	Reason  string
	Summary string
	Workers int
	Call    string
	CallAt  time.Time
}

type JobStatus interface {
	evt.Signal
	jobStatus()
	Job() string
	Prompt() string
	State() string
	Started() time.Time
	Ended() time.Time
	Reason() string
	Summary() string
	Workers() int
	Call() string
	CallAt() time.Time
}

type jobStatus struct {
	evt.Mark
	job     string
	prompt  string
	state   string
	started time.Time
	ended   time.Time
	reason  string
	summary string
	workers int
	call    string
	callAt  time.Time
}

type jobStatusWire struct {
	Job     string
	Prompt  string
	State   string
	Started time.Time
	Ended   time.Time
	Reason  string
	Summary string `json:"Summary,omitempty"`
	Workers int
	Call    string
	CallAt  time.Time
}

func NewJobStatus(in JobStatusInput) JobStatus {
	return jobStatusOf(jobStatusWire{
		Job: in.Job, Prompt: in.Prompt, State: in.State, Started: in.Started,
		Ended: in.Ended, Reason: in.Reason, Summary: in.Summary,
		Workers: in.Workers, Call: in.Call, CallAt: in.CallAt,
	})
}

func jobStatusOf(w jobStatusWire) *jobStatus {
	return &jobStatus{
		job: w.Job, prompt: w.Prompt, state: w.State, started: w.Started,
		ended: w.Ended, reason: w.Reason, summary: w.Summary,
		workers: w.Workers, call: w.Call, callAt: w.CallAt,
	}
}

func (j *jobStatus) jobStatus()         {}
func (j *jobStatus) Job() string        { return j.job }
func (j *jobStatus) Prompt() string     { return j.prompt }
func (j *jobStatus) State() string      { return j.state }
func (j *jobStatus) Started() time.Time { return j.started }
func (j *jobStatus) Ended() time.Time   { return j.ended }
func (j *jobStatus) Reason() string     { return j.reason }
func (j *jobStatus) Summary() string    { return j.summary }
func (j *jobStatus) Workers() int       { return j.workers }
func (j *jobStatus) Call() string       { return j.call }
func (j *jobStatus) CallAt() time.Time  { return j.callAt }

func encodeJobStatus(j *jobStatus) ([]byte, error) {
	return json.Marshal(jobStatusWire{
		Job: j.job, Prompt: j.prompt, State: j.state, Started: j.started,
		Ended: j.ended, Reason: j.reason, Summary: j.summary,
		Workers: j.workers, Call: j.call, CallAt: j.callAt,
	})
}

func decodeJobStatus(raw []byte) (*jobStatus, error) {
	var w jobStatusWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	return jobStatusOf(w), nil
}

func Codec() broadcast.Encoder {
	reg, err := Registry()
	if err != nil {
		panic("room: " + err.Error())
	}
	return broadcast.NewJSONEncoder(reg)
}

func Registry() (broadcast.Registry, error) {
	reg, err := broadcast.Protocol()
	if err != nil {
		return nil, err
	}
	if reg, err = broadcast.Declare[*startChild](reg, "room/start-child", encodeStartChild, decodeStartChild); err != nil {
		return nil, err
	}
	if reg, err = broadcast.Declare[*stopChild](reg, "room/stop-child", encodeStopChild, decodeStopChild); err != nil {
		return nil, err
	}
	if reg, err = broadcast.Declare[*swapChild](reg, "room/swap-child", encodeSwapChild, decodeSwapChild); err != nil {
		return nil, err
	}
	if reg, err = broadcast.Declare[*tableChange](reg, "room/table-change", encodeTableChange, decodeTableChange); err != nil {
		return nil, err
	}
	if reg, err = broadcast.Declare[*jobStatus](reg, "room/job-status", encodeJobStatus, decodeJobStatus); err != nil {
		return nil, err
	}
	return broadcast.Declare[*update](reg, "room/update", encodeUpdate, decodeUpdate)
}

func ParseJobStatus(raw []byte) (JobStatus, error) {
	st, err := decodeJobStatus(raw)
	if err != nil {
		return nil, err
	}
	return st, nil
}

func (s *startChild) MarshalJSON() ([]byte, error)  { return encodeStartChild(s) }
func (s *stopChild) MarshalJSON() ([]byte, error)   { return encodeStopChild(s) }
func (s *swapChild) MarshalJSON() ([]byte, error)   { return encodeSwapChild(s) }
func (s *tableChange) MarshalJSON() ([]byte, error) { return encodeTableChange(s) }
func (u *update) MarshalJSON() ([]byte, error)      { return encodeUpdate(u) }
func (j *jobStatus) MarshalJSON() ([]byte, error)   { return encodeJobStatus(j) }

var (
	_ StartChild  = (*startChild)(nil)
	_ StopChild   = (*stopChild)(nil)
	_ SwapChild   = (*swapChild)(nil)
	_ TableChange = (*tableChange)(nil)
	_ Update      = (*update)(nil)
	_ JobStatus   = (*jobStatus)(nil)
)
