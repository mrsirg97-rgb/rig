package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

type wireQuestion struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Prompt  string   `json:"prompt"`
	Choices []string `json:"choices,omitempty"`
}

type wireRequest struct {
	State     string         `json:"state"`
	Questions []wireQuestion `json:"questions"`
}

type wireAnswer struct {
	Question   string  `json:"question"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
}

type wireReply struct {
	Answers []wireAnswer `json:"answers"`
}

func (d *httpDecider) Decide(ctx context.Context, state string, questions []Question) ([]Answer, error) {
	req := wireRequest{State: state}
	for _, q := range questions {
		req.Questions = append(req.Questions, wireQuestion{ID: q.ID, Kind: q.Kind, Prompt: q.Prompt, Choices: q.Choices})
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
	for _, a := range parsed.Answers {
		if a.Confidence < 0 || a.Confidence > 1 {
			continue
		}
		if a.Value == "" {
			continue
		}
		out = append(out, Answer{Question: a.Question, Value: a.Value, Confidence: a.Confidence, Decider: d.name})
	}
	return out, nil
}

func decodeReply(r io.Reader) (wireReply, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var out wireReply
	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("reply: %w", err)
	}
	return out, nil
}
