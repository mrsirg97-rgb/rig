package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const httpTimeout = 2 * time.Minute

const replyCap = 1 << 20

type HTTPOptions struct {
	URL     string
	Client  *http.Client
	Decider string
}

type httpDecider struct {
	endpoint string
	client   *http.Client
	name     string
}

func NewHTTP(o HTTPOptions) (Decider, error) {
	if strings.TrimSpace(o.URL) == "" {
		return nil, fmt.Errorf("decision: no url")
	}
	u, err := url.Parse(o.URL)
	if err != nil {
		return nil, fmt.Errorf("decision: url %q: %w", o.URL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("decision: url %q: the scheme must be http or https", o.URL)
	}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}
	name := o.Decider
	if name == "" {
		name = u.Host
	}
	return &httpDecider{
		endpoint: strings.TrimSuffix(o.URL, "/") + "/v1/systemone",
		client:   client,
		name:     name,
	}, nil
}

// Laya's wire: the questions ride keyed by their id, each as its type,
// the instructions, and the criteria — a map of label to what it means
// for a choice, the ordered criteria list for a score, nothing for a
// noul.
type wireRequest struct {
	State     string                     `json:"state"`
	Questions map[string]json.RawMessage `json:"questions"`
}

// wireNoul is Laya's name for a yes/no.
const wireNoul = "noul"

type wireAnswer struct {
	Type             string   `json:"type"`
	Choice           string   `json:"choice"`
	Noul             *float64 `json:"noul"`
	Score            *float64 `json:"score"`
	AnswerConfidence float64  `json:"answer_confidence"`
}

// wireReply is the envelope Laya answers with; the model, the usage, the
// routing, and the action block on each answer are ignored, so the decode
// tolerates any field it does not know.
type wireReply struct {
	Answers map[string]wireAnswer `json:"answers"`
}

func (d *httpDecider) Decide(ctx context.Context, state string, questions []Question) ([]Answer, error) {
	req := wireRequest{State: state, Questions: make(map[string]json.RawMessage, len(questions))}
	for _, q := range questions {
		raw, err := marshalQuestion(q)
		if err != nil {
			return nil, fmt.Errorf("decision: %s: %w", d.name, err)
		}
		req.Questions[q.ID] = raw
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("decision: request: %w", err)
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("decision: request: %w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	res, err := d.client.Do(r)
	if err != nil {
		return nil, fmt.Errorf("decision: %s: %w", d.name, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("decision: %s: status %d", d.name, res.StatusCode)
	}
	parsed, err := decodeReply(io.LimitReader(res.Body, replyCap))
	if err != nil {
		return nil, fmt.Errorf("decision: %s: %w", d.name, err)
	}
	var out []Answer
	for id, a := range parsed.Answers {
		value, ok := answerValue(a)
		if !ok {
			continue
		}
		if a.AnswerConfidence < 0 || a.AnswerConfidence > 1 {
			continue
		}
		out = append(out, Answer{Question: id, Value: value, Confidence: a.AnswerConfidence, Decider: d.name})
	}
	return out, nil
}

type wireChoiceQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

type wireScoreQuestion struct {
	Type         string   `json:"type"`
	Instructions string   `json:"instructions"`
	Criteria     []string `json:"criteria,omitempty"`
}

type wireNoulQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
}

func marshalQuestion(q Question) (json.RawMessage, error) {
	switch q.Kind {
	case KindChoice:
		return json.Marshal(wireChoiceQuestion{KindChoice, q.Prompt, described(q)})
	case KindScore:
		return json.Marshal(wireScoreQuestion{KindScore, q.Prompt, q.Criteria})
	case KindYesNo:
		return json.Marshal(wireNoulQuestion{wireNoul, q.Prompt})
	}
	return nil, fmt.Errorf("question %q: kind %q has no wire shape", q.ID, q.Kind)
}

func described(q Question) map[string]string {
	out := make(map[string]string, len(q.Description))
	for label, meaning := range q.Description {
		if meaning != "" {
			out[label] = meaning
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// answerValue reads the value off Laya's answer: a choice names the label,
// a noul is the probability of the yes option so the answer is that side
// (yes at 0.5, no below) and the answer_confidence is the mass on it, a
// score is the expected level. An answer of no known type, or one missing
// its value, is no answer.
func answerValue(a wireAnswer) (string, bool) {
	switch a.Type {
	case KindChoice:
		return a.Choice, a.Choice != ""
	case wireNoul:
		if a.Noul == nil {
			return "", false
		}
		if *a.Noul >= 0.5 {
			return "yes", true
		}
		return "no", true
	case KindScore:
		if a.Score == nil {
			return "", false
		}
		return strconv.FormatFloat(*a.Score, 'f', -1, 64), true
	}
	return "", false
}

func decodeReply(r io.Reader) (wireReply, error) {
	dec := json.NewDecoder(r)
	var out wireReply
	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("reply: %w", err)
	}
	return out, nil
}
