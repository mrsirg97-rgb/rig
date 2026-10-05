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

	"github.com/mrsirg97-rgb/rig/v2/tool"
	"github.com/mrsirg97-rgb/rig/v2/tool/file"
)

const SiteDecide = "decide"

const decideQuestionID = "item"

const unsureUnder = 0.5

type DecideOptions struct {
	Decider  Decider
	Recorder Recorder
	Parallel int
}

type Decide interface {
	tool.Definition
	Exec(ctx context.Context, args json.RawMessage) (string, error)

	Choice(ctx context.Context, prompt string, labels []Label, items []string) (string, error)
	Binary(ctx context.Context, prompt string, items []string) (string, error)
	Score(ctx context.Context, prompt string, criteria, items []string) (string, error)
}

type decideTool struct {
	tool.Definition
	dec      Decider
	rec      Recorder
	parallel int
}

func NewDecide(o DecideOptions) (Decide, error) {
	if o.Decider == nil {
		return nil, errors.New("decision: no decider")
	}
	if o.Parallel <= 0 {
		return nil, fmt.Errorf("decision: parallel %d: the fan-out needs a bound", o.Parallel)
	}
	return &decideTool{Definition: tool.Def("decide"), dec: o.Decider, rec: o.Recorder, parallel: o.Parallel}, nil
}

func (t *decideTool) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	a, err := parseDecideArgs(data)
	if err != nil {
		return "", err
	}
	switch a.Kind {
	case KindChoice:
		return t.Choice(ctx, a.Prompt, a.Labels, a.Items)
	case KindScore:
		return t.Score(ctx, a.Prompt, a.Criteria, a.Items)
	case KindBinary:
		return t.Binary(ctx, a.Prompt, a.Items)
	default:
		return "", fmt.Errorf("decide: kind %q: want choice, binary or score", a.Kind)
	}
}

func (t *decideTool) Choice(ctx context.Context, prompt string, labels []Label, items []string) (string, error) {
	if err := promptBound(prompt); err != nil {
		return "", err
	}
	if len(labels) < 2 {
		return "", fmt.Errorf("decide: choice needs at least two labels, got %d", len(labels))
	}
	seen := make(map[string]bool, len(labels))
	for _, l := range labels {
		if l.Label == "" {
			return "", errors.New("decide: an empty label")
		}
		if seen[l.Label] {
			return "", fmt.Errorf("decide: duplicate label %q", l.Label)
		}
		seen[l.Label] = true
	}
	q := Question{ID: decideQuestionID, Kind: KindChoice, Prompt: prompt}
	q.Choices = make([]string, len(labels))
	for i, l := range labels {
		q.Choices[i] = l.Label
		if l.Description != "" {
			if q.Description == nil {
				q.Description = make(map[string]string, len(labels))
			}
			q.Description[l.Label] = l.Description
		}
	}
	return t.run(ctx, KindChoice, q, labelNames(labels), items)
}

func (t *decideTool) Binary(ctx context.Context, prompt string, items []string) (string, error) {
	if err := promptBound(prompt); err != nil {
		return "", err
	}
	return t.run(ctx, KindBinary, Question{ID: decideQuestionID, Kind: KindBinary, Prompt: prompt}, nil, items)
}

func (t *decideTool) Score(ctx context.Context, prompt string, criteria, items []string) (string, error) {
	if err := promptBound(prompt); err != nil {
		return "", err
	}
	if len(criteria) < 2 {
		return "", fmt.Errorf("decide: score needs at least two criteria, got %d", len(criteria))
	}
	for _, c := range criteria {
		if c == "" {
			return "", errors.New("decide: an empty criterion")
		}
	}
	return t.run(ctx, KindScore, Question{ID: decideQuestionID, Kind: KindScore, Prompt: prompt, Criteria: criteria}, nil, items)
}

func promptBound(prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return errors.New("decide: an empty prompt")
	}
	return nil
}

func itemsBound(items []string) error {
	if len(items) == 0 {
		return errors.New("decide: no items")
	}
	for _, item := range items {
		if item == "" {
			return errors.New("decide: an empty item")
		}
	}
	return nil
}

func (t *decideTool) run(ctx context.Context, kind string, q Question, labels, items []string) (string, error) {
	if err := itemsBound(items); err != nil {
		return "", err
	}
	total := 0
	for _, item := range items {
		total += len(item)
	}
	if total >= file.ReadCap {
		return "", fmt.Errorf("decide: the items total %d bytes, the read ceiling is %d; split the call", total, file.ReadCap)
	}
	states := make([]string, len(items))
	for i, item := range items {
		b, mErr := json.Marshal(decideState{Item: item})
		if mErr != nil {
			return "", fmt.Errorf("decide: item %d: %w", i+1, mErr)
		}
		states[i] = string(b)
	}
	answers, errs := FanOut(ctx, t.dec, t.parallel, q, items)
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
	t.record(ctx, kind, q, states, answers)
	return decideReply(kind, labels, items, answers), nil
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

func (t *decideTool) record(ctx context.Context, kind string, q Question, states []string, answers []Answer) {
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
	Kind     string   `json:"kind"`
	Prompt   string   `json:"prompt"`
	Labels   []Label  `json:"labels"`
	Criteria []string `json:"criteria"`
	Items    []string `json:"items"`
}

type Label struct {
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
	return a, nil
}

func labelNames(labels []Label) []string {
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
