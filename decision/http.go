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

type wireRequest struct {
	State     string                     `json:"state"`
	Questions map[string]json.RawMessage `json:"questions"`
}

const wireNoul = "noul"

type wireAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Noul          *float64           `json:"noul"`
	Score         *float64           `json:"score"`
	Probabilities map[string]float64 `json:"probabilities"`
}

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
		value, conf, ok := answerOf(a)
		if !ok {
			continue
		}
		if conf < 0 || conf > 1 {
			continue
		}
		out = append(out, Answer{Question: id, Value: value, Confidence: conf, Decider: d.name})
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
	case KindBinary:
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

func answerOf(a wireAnswer) (string, float64, bool) {
	switch a.Type {
	case KindChoice:
		p, ok := a.Probabilities[a.Choice]
		if a.Choice == "" || !ok {
			return "", 0, false
		}
		return a.Choice, p, true
	case wireNoul:
		if a.Noul == nil {
			return "", 0, false
		}
		if *a.Noul >= 0.5 {
			return "yes", *a.Noul, true
		}
		return "no", 1 - *a.Noul, true
	case KindScore:
		if a.Score == nil || len(a.Probabilities) == 0 {
			return "", 0, false
		}
		return strconv.FormatFloat(*a.Score, 'f', -1, 64), mass(a.Probabilities), true
	}
	return "", 0, false
}

func mass(p map[string]float64) float64 {
	top := 0.0
	for _, v := range p {
		if v > top {
			top = v
		}
	}
	return top
}

func decodeReply(r io.Reader) (wireReply, error) {
	dec := json.NewDecoder(r)
	var out wireReply
	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("reply: %w", err)
	}
	return out, nil
}
