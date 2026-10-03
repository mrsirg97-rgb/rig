package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/tool"
	"github.com/mrsirg97-rgb/rig/v2/tool/file"
)

const SiteDecide = "decide"

const decideQuestionID = "item"

const unsureUnder = 0.5

const decideGuideline = "when a step is sorting or filtering many items against a question you can state, hand the items to decide instead of reading them."

type DecideOptions struct {
	Decider  Decider
	Recorder Recorder
	Parallel int
}

type Decide struct {
	tool.Definition
	dec      Decider
	rec      Recorder
	parallel int
}

func NewDecide(o DecideOptions) (*Decide, error) {
	if o.Decider == nil {
		return nil, errors.New("decision: no decider")
	}
	if o.Parallel <= 0 {
		return nil, fmt.Errorf("decision: parallel %d: the fan-out needs a bound", o.Parallel)
	}
	return &Decide{Definition: tool.Def("decide"), dec: o.Decider, rec: o.Recorder, parallel: o.Parallel}, nil
}

func (t *Decide) SetParallel(n int) {
	if n <= 0 {
		panic(fmt.Sprintf("decision: parallel %d: the fan-out needs a bound", n))
	}
	t.parallel = n
}

func (t *Decide) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	a, err := parseDecideArgs(data)
	if err != nil {
		return "", err
	}
	q := a.question()
	total := 0
	for _, item := range a.Items {
		total += len(item)
	}
	if total >= file.ReadCap {
		return "", fmt.Errorf("decide: the items total %d bytes, the read ceiling is %d; split the call", total, file.ReadCap)
	}
	states := make([]string, len(a.Items))
	for i, item := range a.Items {
		b, mErr := json.Marshal(decideState{Item: item})
		if mErr != nil {
			return "", fmt.Errorf("decide: item %d: %w", i+1, mErr)
		}
		states[i] = string(b)
	}
	answers, errs := FanOut(ctx, t.dec, t.parallel, q, a.Items)
	for i, err := range errs {
		if err != nil {
			return "", fmt.Errorf("decide: item %d: %w; nothing was sorted and no row was written", i+1, err)
		}
	}
	for i, a := range answers {
		if a.Question != q.ID {
			return "", fmt.Errorf("decide: item %d: the server replied no answer; nothing was sorted and no row was written", i+1)
		}
	}
	t.record(ctx, a.Kind, q, states, answers)
	return decideReply(a.Kind, labelNames(a.Labels), a.Items, answers), nil
}

func FanOut(ctx context.Context, dec Decider, parallel int, q Question, states []string) ([]Answer, []error) {
	answers := make([]Answer, len(states))
	errs := make([]error, len(states))
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i, state := range states {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int, state string) {
			defer wg.Done()
			defer func() { <-sem }()
			out, err := dec.Decide(ctx, state, []Question{q})
			if err != nil {
				errs[i] = err
				return
			}
			for _, a := range out {
				if a.Question == q.ID {
					answers[i] = a
					return
				}
			}
		}(i, state)
	}
	wg.Wait()
	return answers, errs
}

func (t *Decide) record(ctx context.Context, kind string, q Question, states []string, answers []Answer) {
	if t.rec == nil {
		return
	}
	for i, a := range answers {
		conf := a.Confidence
		t.rec.Record(ctx, Final{
			Site:       SiteDecide,
			State:      states[i],
			Question:   q,
			Answer:     a.Value,
			Confidence: &conf,
			Unsure:     kind == KindChoice && a.Confidence < unsureUnder,
			Decider:    a.Decider,
		})
	}
}

type decideArgs struct {
	Kind     string        `json:"kind"`
	Prompt   string        `json:"prompt"`
	Labels   []decideLabel `json:"labels"`
	Criteria []string      `json:"criteria"`
	Items    []string      `json:"items"`
}

type decideLabel struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type decideState struct {
	Item string `json:"item"`
}

func parseDecideArgs(data json.RawMessage) (decideArgs, error) {
	var a decideArgs
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, fmt.Errorf("decide: args: %w", err)
	}
	switch a.Kind {
	case KindChoice, KindScore, KindYesNo:
	default:
		return a, fmt.Errorf("decide: kind %q: want choice, yesno or score", a.Kind)
	}
	if strings.TrimSpace(a.Prompt) == "" {
		return a, errors.New("decide: an empty prompt")
	}
	switch a.Kind {
	case KindChoice:
		if len(a.Labels) < 2 {
			return a, fmt.Errorf("decide: choice needs at least two labels, got %d", len(a.Labels))
		}
		seen := make(map[string]bool, len(a.Labels))
		for _, l := range a.Labels {
			if l.Label == "" {
				return a, errors.New("decide: an empty label")
			}
			if seen[l.Label] {
				return a, fmt.Errorf("decide: duplicate label %q", l.Label)
			}
			seen[l.Label] = true
		}
	case KindScore:
		if len(a.Criteria) < 2 {
			return a, fmt.Errorf("decide: score needs at least two criteria, got %d", len(a.Criteria))
		}
		for _, c := range a.Criteria {
			if c == "" {
				return a, errors.New("decide: an empty criterion")
			}
		}
	}
	if len(a.Items) == 0 {
		return a, errors.New("decide: no items")
	}
	for _, item := range a.Items {
		if item == "" {
			return a, errors.New("decide: an empty item")
		}
	}
	return a, nil
}

func (a decideArgs) question() Question {
	q := Question{ID: decideQuestionID, Kind: a.Kind, Prompt: a.Prompt}
	switch a.Kind {
	case KindChoice:
		q.Choices = make([]string, len(a.Labels))
		for i, l := range a.Labels {
			q.Choices[i] = l.Label
			if l.Description != "" {
				if q.Description == nil {
					q.Description = make(map[string]string, len(a.Labels))
				}
				q.Description[l.Label] = l.Description
			}
		}
	case KindScore:
		q.Criteria = a.Criteria
	}
	return q
}

func labelNames(labels []decideLabel) []string {
	out := make([]string, len(labels))
	for i, l := range labels {
		out[i] = l.Label
	}
	return out
}

func decideReply(kind string, labels, items []string, answers []Answer) string {
	unsure := unsureIDs(kind, answers)
	unsureSet := make(map[int]bool, len(unsure))
	for _, i := range unsure {
		unsureSet[i] = true
	}
	byValue := make(map[string][]int, len(labels))
	order := make([]string, 0, len(labels))
	for i, a := range answers {
		if unsureSet[i] {
			continue
		}
		if _, ok := byValue[a.Value]; !ok {
			order = append(order, a.Value)
		}
		byValue[a.Value] = append(byValue[a.Value], i)
	}
	rank := make(map[string]int, len(labels))
	for i, l := range labels {
		rank[l] = i
	}
	extra := 0
	for _, v := range order {
		if _, ok := rank[v]; !ok {
			rank[v] = len(labels) + extra
			extra++
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return rank[order[i]] < rank[order[j]] })
	lines := make([]string, 0, len(order)+1)
	for _, v := range order {
		ids := byValue[v]
		parts := make([]string, 0, len(ids))
		for _, i := range ids {
			parts = append(parts, fmt.Sprintf("#%d %s", i+1, firstLine(items[i])))
		}
		lines = append(lines, fmt.Sprintf("%s (%d): %s", v, len(ids), strings.Join(parts, "; ")))
	}
	if len(unsure) > 0 {
		lines = append(lines, fmt.Sprintf("unsure (%d) — the top probability is under one half; judge these yourself", len(unsure)))
		blocks := make([]string, 0, len(unsure))
		for _, i := range unsure {
			blocks = append(blocks, fmt.Sprintf("#%d: %s", i+1, items[i]))
		}
		lines = append(lines, strings.Join(blocks, "\n\n"))
	}
	return strings.Join(lines, "\n")
}

func unsureIDs(kind string, answers []Answer) []int {
	if kind != KindChoice {
		return nil
	}
	var out []int
	for i, a := range answers {
		if a.Confidence < unsureUnder {
			out = append(out, i)
		}
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func Guide() core.ToolMiddleware {
	return guideLink{}
}

type guideLink struct{}

func (guideLink) Wrap(next core.ToolExec) core.ToolExec { return next }

func (guideLink) Guidelines() string { return decideGuideline }
