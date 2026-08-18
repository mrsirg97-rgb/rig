// Package compact is the first non-passthrough ContextPolicy
// (SPEC_COMPACT): below the trigger it is the passthrough,
// byte-identical; at the trigger it rewrites the older transcript into a
// summary through the same core.Provider and returns system + summary +
// kept tail. It also carries the overflow decorator (7): a provider
// fault that names context length triggers one compact-and-retry, once,
// then surfaces. Stdlib only; the summary prompt is one embedded file.
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

// SummaryMarker makes the summary rows self-describing: they are exactly
// the user rows that start with it (decision 5). Grep is the interface.
const SummaryMarker = "[compaction] "

//go:embed summary_prompt.txt
var summaryPrompt string

type policy struct {
	provider    core.Provider // the inner instance (the loop's, through the decorator)
	fe          core.Frontend // the kernel's frontend (the recorder), as the root wires it
	s           *core.Session // the root's session (k.Session)
	system      string        // the root's assembled system prompt
	row         models.Model
	autoReflect func(ctx context.Context, summary string) error // decision 6; absent = the seam off

	mu      sync.Mutex
	factor  float64 // 1.0 until the first report (decision 4)
	lastKey int     // the transcript length at the last compact attempt (the once budget, 7)
}

// options carries the policy's wiring seams.
type options struct {
	autoReflect func(ctx context.Context, summary string) error
}

// Option configures the policy (decision 6's seam).
type Option func(*options)

// WithAutoReflect hands the summary to rem's AutoReflect (decision 6):
// the policy does not import store/rem — a leaf calling a leaf through a
// named callback, the DI seam, not a dependency. Absent = the seam off.
func WithAutoReflect(fn func(ctx context.Context, summary string) error) Option {
	return func(o *options) { o.autoReflect = fn }
}

// New builds the policy. m is checked at construction (loud); the
// baseline of the once budget (7) is the transcript length at
// construction — the root builds the session before the policy, so a
// resumed session starts at that baseline.
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

// Assemble is the trigger path (decisions 1, 3, 4): below the trigger it
// is the passthrough, byte-identical; at the trigger (strict: size >
// Window - Reserve) it runs the compact action and returns the
// passthrough output on the new transcript. A compact that cannot help
// (a single oversized last message, an input that does not fit) returns
// the passthrough or fails loud, by name.
func (p *policy) Assemble(ctx context.Context, s *core.Session) ([]core.Message, error) {
	if p.sizeOf(p.assemble(s)) <= p.row.Window-p.row.Reserve {
		return p.assemble(s), nil
	}
	evs, compacted, err := p.compact(ctx)
	if err != nil {
		return nil, err
	}
	if !compacted {
		// nothing to drop: the passthrough, named (decision 3's boundary)
		return p.assemble(s), nil
	}
	p.spendBudget()
	// the trigger path delivers the event to the frontend the policy holds
	// (the recorder); the overflow path (7) delivers it on the stream
	// instead — the compact action returns it, the caller owns the
	// delivery, so emission is exactly once per compaction (decision 5).
	p.fe.Notify(evs)
	return p.assemble(s), nil
}

// Compact is the forced seam (SPEC_COMMANDS 3; the scope promise 8's
// owed): the same internal action as the trigger path — split, summarize,
// rewrite, reflect — with the trigger bypassed on purpose: the trigger is
// the model's window math, the verb is the user's judgment. It spends the
// once budget as the trigger path does, so the next context-length fault
// on the un-grown transcript surfaces without recovery. The action never
// emits (decision 5) — the caller owns delivery, exactly once.
func (p *policy) Compact(ctx context.Context) (core.Compacted, bool, error) {
	ev, compacted, err := p.compact(ctx)
	if compacted {
		p.spendBudget()
	}
	return ev, compacted, err
}

// assemble is the passthrough: the system prompt (when set) plus the
// transcript, verbatim.
func (p *policy) assemble(s *core.Session) []core.Message {
	msgs := make([]core.Message, 0, len(s.Messages)+1)
	if p.system != "" {
		msgs = append(msgs, core.Message{Role: core.RoleSystem, Content: p.system})
	}
	return append(msgs, s.Messages...)
}

// compact is the shared compact action (decision 1): split, summarize
// through the inner provider (not through the decorator — a fault in the
// summary call surfaces as an Assemble error and never recursively
// compacts), rewrite the session transcript to [summary row] + tail, and
// fire the AutoReflect seam (swallowed — decision 6). It returns the
// Compacted event without delivering it — the caller owns the emission
// (Assemble to p.fe on the trigger path, the decorator on the stream in
// the overflow path, decision 5), so a compaction emits exactly once. It
// is impure by design: one rewrite, one provider call, one event, one
// reflection; and not idempotent — a second compaction is a fold (3).
func (p *policy) compact(ctx context.Context) (core.Compacted, bool, error) {
	p.mu.Lock()
	factor := p.factor
	p.mu.Unlock()

	older, tail := split(p.s.Messages, factor, p.row.KeepRecent)
	if len(older) == 0 {
		return core.Compacted{}, false, nil // nothing to drop
	}
	input := SummaryInput(older)
	p.mu.Lock()
	factor = p.factor
	p.mu.Unlock()
	est := int(float64(Estimate(input)) * factor)
	if budget := p.row.Window - est; budget <= 0 {
		return core.Compacted{}, false, fmt.Errorf("compact: %s: the summary input alone does not fit the window: window %d, estimate %d",
			p.row.ID, p.row.Window, est)
	}

	summary, usage, err := p.summarize(ctx, input, minInt(p.row.MaxTokens, p.row.Window-est))
	if err != nil {
		return core.Compacted{}, false, err
	}

	// the rewrite (decision 1): the transcript becomes [summary row] +
	// tail; Session.Files is untouched, so drift checks survive.
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
		_ = p.autoReflect(ctx, summary) // fire-and-forget (6): a store failure never fails the turn
	}
	return ev, true, nil
}

// summarize runs the one summary call (3): the short system role plus
// one user message carrying the older prefix as quoted transcript data
// and the prompt's instruction — data, not a live conversation, so the
// model summarizes it rather than continuing it (3). No tools; the
// MaxTokens clamp is 3's honest budget. The reasoning effort is the
// row's Effort — the one call whose thinking nobody reads takes the
// row's budget where the operator set one (SPEC_CONFIG 4) — with
// "medium" the field's default: the 0.2.0 behavior, now the default a
// row constructed without one carries (a provider that does not know
// the field ignores it).
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

// recoveryOwed is the once budget (decision 7): structural, not a
// counter — a compact is owed only if the transcript has grown since the
// last compact attempt (the baseline is the length at construction, so a
// resumed session starts there). A second context-length fault against
// the same transcript is no new information — it surfaces.
func (p *policy) recoveryOwed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.s.Messages) > p.lastKey
}

// spendBudget keys the last compact attempt to the transcript length.
func (p *policy) spendBudget() {
	p.mu.Lock()
	p.lastKey = len(p.s.Messages)
	p.mu.Unlock()
}

// calibrate is the delta-only update (decision 4): reported - anchor
// isolates the delta exactly (the anchor absorbs that call's
// system+spec, the session's constant between calls, and the specs never
// enter the ratio); a request with no anchor or an empty delta leaves
// the factor as it is — the whole-request ratio carries the constant,
// and staying at 1.0 beats learning a constant. The clamp is a guard
// against a server that reports a total where a prompt is expected.
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
	if deltaEst == 0 {
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
	if f > 4.0 {
		f = 4.0
	}
	p.mu.Lock()
	p.factor = f
	p.mu.Unlock()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
