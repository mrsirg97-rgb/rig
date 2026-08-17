package core_test

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
)

// The widened seam: a plain function adapts and wraps exactly as before.
func TestToolMiddlewareFuncAdaptsToTheSeam(t *testing.T) {
	wrapped := false
	f := core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec {
		return func(ctx context.Context, call core.ToolCall) (string, error) {
			wrapped = true
			return "wrapped", nil
		}
	})
	var mw core.ToolMiddleware = f
	out, err := mw.Wrap(nil)(context.Background(), core.ToolCall{Name: "bash"})
	if err != nil || out != "wrapped" || !wrapped {
		t.Fatalf("adapter did not wrap: %q %v %v", out, err, wrapped)
	}
}

type bothCaps struct{ core.ToolMiddlewareFunc }

func (bothCaps) TurnStart(ctx context.Context, s *core.Session) {}
func (bothCaps) Guidelines() string                             { return "prose" }

var (
	_ core.TurnObserver         = bothCaps{}
	_ core.GuidelineContributor = bothCaps{}
)

// The optional capabilities are assertion-checked, not forced: a plain
// adapter has neither, a participant can carry both.
func TestOptionalCapabilitiesAreAssertions(t *testing.T) {
	var mw core.ToolMiddleware = core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec { return next })
	if _, ok := mw.(core.TurnObserver); ok {
		t.Fatal("a wrap-only middleware must not be a TurnObserver")
	}
	if _, ok := mw.(core.GuidelineContributor); ok {
		t.Fatal("a wrap-only middleware must not be a GuidelineContributor")
	}
}

// The interrupt handle: threaded under a typed key, recovered by identity.
func TestInterruptThreadsAndRecoversByIdentity(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, ok := core.InterruptFrom(base); ok {
		t.Fatal("a bare ctx must carry no interrupt handle")
	}
	carry := core.WithInterrupt(base, cancel)
	back, ok := core.InterruptFrom(carry)
	if !ok {
		t.Fatal("the handle must recover from the threaded ctx")
	}
	back() // a different function would not reach the same cancel
	if base.Err() == nil {
		t.Fatal("the recovered handle must cancel the same context")
	}
}

type foreignKey struct{}

// A foreign value under a foreign key is not a handle: the assertion
// returns false and the Frontend is unaffected.
func TestInterruptIgnoresForeignValues(t *testing.T) {
	if _, ok := core.InterruptFrom(context.WithValue(context.Background(), foreignKey{}, "not a cancel")); ok {
		t.Fatal("a foreign ctx value must not read as an interrupt handle")
	}
	if _, ok := core.InterruptFrom(context.Background()); ok {
		t.Fatal("a value-less ctx must read as no handle")
	}
}

// The hardening vocabulary exists, is Event, and carries the named
// reasons. Consumers (loop, frontends, recorder) switch on these.
func TestHardeningVocabulary(t *testing.T) {
	var (
		_ core.Event = core.ReasoningDelta{Text: "t"}
		_ core.Event = core.ToolStart{Call: core.ToolCall{ID: "c1"}}
		_ core.Event = core.ToolResult{ID: "c1", Content: "out"}
		_ core.Event = core.TurnEnd{Reason: core.TurnOver}
		_ core.Event = core.TestEvent{Name: "x"}
		_ core.Event = core.Compacted{Summary: "s"} // SPEC_COMPACT 5: additive, the compat rule holds
	)
	if core.TurnOver != "over" || core.TurnFault != "fault" || core.TurnInterrupt != "interrupt" {
		t.Fatalf("turn reasons = %q/%q/%q, want over/fault/interrupt", core.TurnOver, core.TurnFault, core.TurnInterrupt)
	}
}

// Usage carries the cache fields; zero is the pre-transport value.
func TestUsageCarriesCacheFields(t *testing.T) {
	u := core.Usage{Prompt: 922, Completion: 10, CacheRead: 918, CacheWrite: 0}
	if u.Prompt != 922 || u.Completion != 10 || u.CacheRead != 918 || u.CacheWrite != 0 {
		t.Fatalf("usage shape drifted: %+v", u)
	}
	if (core.Usage{}).CacheRead != 0 || (core.Usage{}).CacheWrite != 0 {
		t.Fatal("a zero usage must report zero cache")
	}
}

// Message carries the reasoning block; assistant turns only, empty when the
// model did not think.
func TestMessageCarriesReasoning(t *testing.T) {
	m := core.Message{Role: core.RoleAssistant, Content: "answer", Reasoning: "thought"}
	if m.Reasoning != "thought" || m.Content != "answer" {
		t.Fatalf("message shape drifted: %+v", m)
	}
}

// Message carries the anchor (SPEC_COMPACT 4, L8): the server-reported
// prompt+completion on an assistant message; 0 when unreported.
func TestMessageCarriesContextTokens(t *testing.T) {
	m := core.Message{Role: core.RoleAssistant, Content: "answer", ContextTokens: 1234}
	if m.ContextTokens != 1234 {
		t.Fatalf("message shape drifted: %+v", m)
	}
	if (core.Message{}).ContextTokens != 0 {
		t.Fatal("an unreported count must be zero")
	}
}

// Request carries MaxTokens (SPEC_COMPACT 8): 0 = the provider's default;
// a provider that does not know it ignores it.
func TestRequestCarriesMaxTokens(t *testing.T) {
	r := core.Request{MaxTokens: 8192}
	if r.MaxTokens != 8192 {
		t.Fatalf("request shape drifted: %+v", r)
	}
	if (core.Request{}).MaxTokens != 0 {
		t.Fatal("an unset budget must be zero (the provider's default)")
	}
}
