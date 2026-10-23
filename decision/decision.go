package decision

import (
	"context"
	"encoding/json"
)

const (
	KindChoice = "choice"
	KindScore  = "score"
	KindBinary = "binary"

	kindLegacyBinary = "yesno"
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

type QuestionInput struct {
	ID     string
	Kind   string
	Prompt string
}

type QuestionOption func(*question)

type Question interface {
	ID() string
	Kind() string
	Prompt() string
	Choices() []string
	Description() map[string]string
	Criteria() []string
}

type question struct {
	id          string
	kind        string
	prompt      string
	choices     []string
	description map[string]string
	criteria    []string
}

type questionWire struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Prompt      string            `json:"prompt"`
	Choices     []string          `json:"choices,omitempty"`
	Description map[string]string `json:"description,omitempty"`
	Criteria    []string          `json:"criteria,omitempty"`
}

func WithChoices(choices ...string) QuestionOption {
	return func(q *question) { q.choices = append([]string(nil), choices...) }
}

func WithDescription(description map[string]string) QuestionOption {
	return func(q *question) { q.description = copyDescription(description) }
}

func WithCriteria(criteria ...string) QuestionOption {
	return func(q *question) { q.criteria = append([]string(nil), criteria...) }
}

func NewQuestion(in QuestionInput, opts ...QuestionOption) Question {
	q := &question{id: in.ID, kind: kindOf(in.Kind), prompt: in.Prompt}
	for _, opt := range opts {
		opt(q)
	}
	return q
}

func Choice(id, prompt string, choices ...string) Question {
	return NewQuestion(QuestionInput{ID: id, Kind: KindChoice, Prompt: prompt}, WithChoices(choices...))
}

func Score(id, prompt string, criteria ...string) Question {
	return NewQuestion(QuestionInput{ID: id, Kind: KindScore, Prompt: prompt}, WithCriteria(criteria...))
}

func Binary(id, prompt string) Question {
	return NewQuestion(QuestionInput{ID: id, Kind: KindBinary, Prompt: prompt})
}

func ParseQuestion(raw []byte) (Question, error) {
	var w questionWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	q := &question{id: w.ID, kind: kindOf(w.Kind), prompt: w.Prompt}
	q.choices = append([]string(nil), w.Choices...)
	q.criteria = append([]string(nil), w.Criteria...)
	q.description = copyDescription(w.Description)
	return q, nil
}

func kindOf(kind string) string {
	if kind == kindLegacyBinary {
		return KindBinary
	}
	return kind
}

func copyDescription(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (q *question) ID() string                     { return q.id }
func (q *question) Kind() string                   { return q.kind }
func (q *question) Prompt() string                 { return q.prompt }
func (q *question) Choices() []string              { return append([]string(nil), q.choices...) }
func (q *question) Description() map[string]string { return copyDescription(q.description) }
func (q *question) Criteria() []string             { return append([]string(nil), q.criteria...) }

func (q *question) MarshalJSON() ([]byte, error) {
	return json.Marshal(questionWire{
		ID: q.id, Kind: q.kind, Prompt: q.prompt,
		Choices: q.choices, Description: q.description, Criteria: q.criteria,
	})
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
