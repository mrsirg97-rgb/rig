package compact

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/models"
)

const SummaryMarker = "[compaction] "

//go:embed summary_prompt.txt
var summaryPrompt string

type policy struct {
	provider    core.Provider
	fe          core.Frontend
	s           *core.Session
	system      string
	row         models.Model
	autoReflect func(ctx context.Context, summary string) error

	mu      sync.Mutex
	factor  float64
	lastKey int
}

type options struct {
	autoReflect func(ctx context.Context, summary string) error
}

type Option func(*options)

func WithAutoReflect(fn func(ctx context.Context, summary string) error) Option {
	return func(o *options) { o.autoReflect = fn }
}

func New(provider core.Provider, fe core.Frontend, s *core.Session, system string, m models.Model, opts ...Option) (*policy, error) {
	if provider == nil {
		return nil, errors.New("compact: nil provider")
	}
	if fe == nil {
		return nil, errors.New("compact: nil frontend")
	}
	if s == nil {
		return nil, errors.New("compact: nil session")
	}
	if err := m.Check(); err != nil {
		return nil, err
	}
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}
	return &policy{
		provider:    provider,
		fe:          fe,
		s:           s,
		system:      system,
		row:         m,
		autoReflect: o.autoReflect,
		factor:      1.0,
		lastKey:     len(s.Messages),
	}, nil
}

func (p *policy) Assemble(ctx context.Context, s *core.Session) ([]core.Message, error) {
	if p.sizeOf(p.assemble(s)) <= p.row.Window-p.row.Reserve {
		return p.assemble(s), nil
	}
	evs, compacted, err := p.compact(ctx)
	if err != nil {
		return nil, err
	}
	if !compacted {
		return p.assemble(s), nil
	}
	p.spendBudget()
	p.fe.Notify(evs)
	return p.assemble(s), nil
}

func (p *policy) Compact(ctx context.Context) (core.Compacted, bool, error) {
	ev, compacted, err := p.compact(ctx)
	if compacted {
		p.spendBudget()
	}
	return ev, compacted, err
}

func (p *policy) assemble(s *core.Session) []core.Message {
	msgs := make([]core.Message, 0, len(s.Messages)+1)
	if p.system != "" {
		msgs = append(msgs, core.Message{Role: core.RoleSystem, Content: p.system})
	}
	return append(msgs, s.Messages...)
}

func (p *policy) compact(ctx context.Context) (core.Compacted, bool, error) {
	p.mu.Lock()
	factor := p.factor
	p.mu.Unlock()

	older, tail := split(p.s.Messages, factor, p.row.KeepRecent)
	if len(older) == 0 {
		return core.Compacted{}, false, nil
	}
	input := SummaryInput(older)
	p.mu.Lock()
	factor = p.factor
	p.mu.Unlock()
	est := int(float64(Estimate(input)) * factor)
	if floor := p.summaryFloor(); p.row.Window-est < floor {
		k := fitPrefix(older, factor, p.row.Window-floor)
		if k == 0 {
			return core.Compacted{}, false, fmt.Errorf("compact: %s: the summary input alone does not fit the window: window %d, estimate %d",
				p.row.ID, p.row.Window, est)
		}
		rest := older[k:]
		older = older[:k]
		kept := make([]core.Message, 0, len(rest)+len(tail))
		kept = append(kept, rest...)
		tail = append(kept, tail...)
		input = SummaryInput(older)
		est = int(float64(Estimate(input)) * factor)
	}

	p.fe.Notify(core.Compacting{})
	summary, usage, err := p.summarize(ctx, input, minInt(p.row.MaxTokens, p.row.Window-est))
	if err != nil {
		return core.Compacted{}, false, err
	}

	row := core.Message{Role: core.RoleUser, Content: SummaryMarker + summary}
	newMsgs := make([]core.Message, 0, len(tail)+1)
	newMsgs = append(newMsgs, row)
	newMsgs = append(newMsgs, tail...)
	p.s.Messages = newMsgs

	ev := core.Compacted{
		Summary: row.Content,
		Dropped: int(float64(Estimate(older)) * factor),
		Kept:    int(float64(Estimate(tail)) * factor),
		Usage:   usage,
	}
	if p.autoReflect != nil {
		_ = p.autoReflect(ctx, summary)
	}
	return ev, true, nil
}

func (p *policy) summarize(ctx context.Context, input []core.Message, maxTokens int) (string, core.Usage, error) {
	effort := p.row.Effort
	if effort == "" {
		effort = "medium"
	}
	ch, err := p.provider.Stream(ctx, core.Request{Messages: input, MaxTokens: maxTokens, ReasoningEffort: effort})
	if err != nil {
		return "", core.Usage{}, fmt.Errorf("compact: summary call: %w", err)
	}
	var (
		body  strings.Builder
		usage core.Usage
		done  bool
		fault error
	)
	for ev := range ch {
		switch e := ev.(type) {
		case core.TextDelta:
			body.WriteString(e.Text)
		case core.Done:
			done = true
			usage = e.Usage
		case core.Fault:
			fault = e.Err
		}
	}
	if fault != nil {
		return "", core.Usage{}, fmt.Errorf("compact: summary call: %w", fault)
	}
	if !done {
		return "", core.Usage{}, errors.New("compact: the summary call's stream ended without Done")
	}
	text := body.String()
	if strings.TrimSpace(text) == "" {
		return "", core.Usage{}, errors.New("compact: the summary call returned no text")
	}
	return text, usage, nil
}

func (p *policy) recoveryOwed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.s.Messages) > p.lastKey
}

func (p *policy) spendBudget() {
	p.mu.Lock()
	p.lastKey = len(p.s.Messages)
	p.mu.Unlock()
}

func (p *policy) calibrate(req core.Request, u core.Usage) {
	if u.Prompt <= 0 {
		return
	}
	msgs := req.Messages
	idx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].ContextTokens > 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	deltaEst := Estimate(msgs[idx+1:])
	if deltaEst == 0 || deltaEst*50 < msgs[idx].ContextTokens {
		return
	}
	ratio := float64(u.Prompt-msgs[idx].ContextTokens) / float64(deltaEst)
	if ratio < 0 {
		ratio = 0
	}
	f := ratio
	if f < 0.5 {
		f = 0.5
	}
	if f > 2.0 {
		f = 2.0
	}
	p.mu.Lock()
	p.factor = f
	p.mu.Unlock()
}

func (p *policy) summaryFloor() int {
	floor := p.row.Reserve / 4
	if floor > 256 {
		floor = 256
	}
	if floor < 1 {
		floor = 1
	}
	return floor
}

func fitPrefix(older []core.Message, factor float64, limit int) int {
	fits := func(k int) bool {
		return int(float64(Estimate(SummaryInput(older[:k])))*factor) <= limit
	}
	lo, hi := 0, len(older)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if fits(mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	k := lo
	for k > 0 && k < len(older) && older[k].Role == core.RoleTool {
		j := callIn(older[:k], older[k].ToolID)
		if j < 0 {
			break
		}
		k = j
	}
	return k
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
