package decision

import "context"

const (
	KindChoice = "choice"
	KindScore  = "score"
	KindBinary = "binary"
)

const (
	StatusFinal    = "final"
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusDenied   = "denied"
)

const (
	SiteApprove   = "approve"
	SitePerm      = "perm"
	SitePaths     = "paths"
	SiteGuard     = "guard"
	SiteScheduler = "scheduler"
	SiteBash      = "bash"
	SitePack      = "pack"
)

type Question struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Prompt      string            `json:"prompt"`
	Choices     []string          `json:"choices,omitempty"`
	Description map[string]string `json:"description,omitempty"`
	Criteria    []string          `json:"criteria,omitempty"`
}

type Answer struct {
	Question   string  `json:"question"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	Decider    string  `json:"decider"`
}

type Decider interface {
	Decide(ctx context.Context, state string, questions []Question) ([]Answer, error)
}

type Final struct {
	Scope      string
	Site       string
	State      string
	Question   Question
	Answer     string
	Confidence *float64
	Unsure     bool
	Decider    string
}

type Recorder interface {
	Record(ctx context.Context, f Final)
}

// Deny records the one refusal row: the gate speaks for itself, so the
// site is both the row's site and its decider. A nil recorder is no row.
func Deny(ctx context.Context, rec Recorder, site, state string, q Question, answer string) {
	if rec == nil {
		return
	}
	rec.Record(ctx, Final{
		Site:     site,
		State:    state,
		Question: q,
		Answer:   answer,
		Decider:  site,
	})
}

// FirstRecorder is the middleware constructor's door: a variadic seam
// carries zero or one recorder.
func FirstRecorder(rec ...Recorder) Recorder {
	if len(rec) == 0 {
		return nil
	}
	return rec[0]
}

func Choice(id, prompt string, choices ...string) Question {
	return Question{ID: id, Kind: KindChoice, Prompt: prompt, Choices: choices}
}

func Score(id, prompt string, criteria ...string) Question {
	return Question{ID: id, Kind: KindScore, Prompt: prompt, Criteria: criteria}
}

func Binary(id, prompt string) Question {
	return Question{ID: id, Kind: KindBinary, Prompt: prompt}
}
