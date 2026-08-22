package core_test

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
)

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

func TestOptionalCapabilitiesAreAssertions(t *testing.T) {
	var mw core.ToolMiddleware = core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec { return next })
	if _, ok := mw.(core.TurnObserver); ok {
		t.Fatal("a wrap-only middleware must not be a TurnObserver")
	}
	if _, ok := mw.(core.GuidelineContributor); ok {
		t.Fatal("a wrap-only middleware must not be a GuidelineContributor")
	}
}

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
	back()
	if base.Err() == nil {
		t.Fatal("the recovered handle must cancel the same context")
	}
}

type foreignKey struct{}

func TestInterruptIgnoresForeignValues(t *testing.T) {
	if _, ok := core.InterruptFrom(context.WithValue(context.Background(), foreignKey{}, "not a cancel")); ok {
		t.Fatal("a foreign ctx value must not read as an interrupt handle")
	}
	if _, ok := core.InterruptFrom(context.Background()); ok {
		t.Fatal("a value-less ctx must read as no handle")
	}
}

func TestHardeningVocabulary(t *testing.T) {
	var (
		_ core.Event = core.ReasoningDelta{Text: "t"}
		_ core.Event = core.ToolStart{Call: core.ToolCall{ID: "c1"}}
		_ core.Event = core.ToolResult{ID: "c1", Content: "out"}
		_ core.Event = core.TurnEnd{Reason: core.TurnOver}
		_ core.Event = core.TestEvent{Name: "x"}
		_ core.Event = core.Compacted{Summary: "s"}
		_ core.Event = core.Compacting{}
	)
	if core.TurnOver != "over" || core.TurnFault != "fault" || core.TurnInterrupt != "interrupt" {
		t.Fatalf("turn reasons = %q/%q/%q, want over/fault/interrupt", core.TurnOver, core.TurnFault, core.TurnInterrupt)
	}
}

func TestUsageCarriesCacheFields(t *testing.T) {
	u := core.Usage{Prompt: 922, Completion: 10, CacheRead: 918, CacheWrite: 0}
	if u.Prompt != 922 || u.Completion != 10 || u.CacheRead != 918 || u.CacheWrite != 0 {
		t.Fatalf("usage shape drifted: %+v", u)
	}
	if (core.Usage{}).CacheRead != 0 || (core.Usage{}).CacheWrite != 0 {
		t.Fatal("a zero usage must report zero cache")
	}
}

func TestMessageCarriesReasoning(t *testing.T) {
	m := core.Message{Role: core.RoleAssistant, Content: "answer", Reasoning: "thought"}
	if m.Reasoning != "thought" || m.Content != "answer" {
		t.Fatalf("message shape drifted: %+v", m)
	}
}

func TestMessageCarriesContextTokens(t *testing.T) {
	m := core.Message{Role: core.RoleAssistant, Content: "answer", ContextTokens: 1234}
	if m.ContextTokens != 1234 {
		t.Fatalf("message shape drifted: %+v", m)
	}
	if (core.Message{}).ContextTokens != 0 {
		t.Fatal("an unreported count must be zero")
	}
}

func TestRequestCarriesMaxTokens(t *testing.T) {
	r := core.Request{MaxTokens: 8192}
	if r.MaxTokens != 8192 {
		t.Fatalf("request shape drifted: %+v", r)
	}
	if (core.Request{}).MaxTokens != 0 {
		t.Fatal("an unset budget must be zero (the provider's default)")
	}
}
