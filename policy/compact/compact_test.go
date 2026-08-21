package compact_test

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/models"
	"github.com/mrsirg97-rgb/rig/policy"
	compact "github.com/mrsirg97-rgb/rig/policy/compact"
	"github.com/mrsirg97-rgb/rig/store"
	remstore "github.com/mrsirg97-rgb/rig/store/rem"
)

// testRow is the spec's worker profile scaled to fixture sizes
// (decision 2's shape; the numbers are the test's).
var testRow = models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 200}

// TestBelowTriggerIsPassthroughByteIdentical (SPEC_COMPACT, named): at
// exactly Window - Reserve the output deep-equals policy.Passthrough on
// the same session; one over, the same fixture compacts. Both the
// anchored shape (4) and the anchorless fresh-session shape.
func TestBelowTriggerIsPassthroughByteIdentical(t *testing.T) {
	const system = "S"

	t.Run("anchored at the boundary is passthrough", func(t *testing.T) {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 400)})
		s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 200), ContextTokens: 800})
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 400)})
		// size = anchor 800 + est(delta 400B = 100) = 900 = Window - Reserve
		pol, err := compact.New(&scriptedProvider{}, &captureFrontend{}, s, system, testRow)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		got, err := pol.Assemble(context.Background(), s)
		if err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		want, err := policy.Passthrough(system).Assemble(context.Background(), s)
		if err != nil {
			t.Fatalf("Passthrough: %v", err)
		}
		if len(got) != len(want) {
			t.Fatalf("got %d messages, want %d (the boundary must not compact)", len(got), len(want))
		}
		for i := range want {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Fatalf("message %d = %+v, want byte-identical %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("anchored one over compacts", func(t *testing.T) {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 400)})
		s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 200), ContextTokens: 801})
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 400)})
		// size = 801 + 100 = 901 > 900: the trigger is strict
		prov := &scriptedProvider{turns: []scriptedTurn{{
			events: []core.Event{core.TextDelta{Text: "SUMMARY"}, core.Done{Usage: core.Usage{Prompt: 10, Completion: 1}}},
		}}}
		fe := &captureFrontend{}
		pol, err := compact.New(prov, fe, s, "S", testRow)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		// the older prefix (the first user message) was summarized; the
		// tail (the assistant + its result, the pair kept whole) survives.
		if len(s.Messages) != 3 {
			t.Fatalf("transcript = %d messages, want the summary + the kept tail", len(s.Messages))
		}
		if s.Messages[0].Role != core.RoleUser || !strings.HasPrefix(s.Messages[0].Content, compact.SummaryMarker) {
			t.Fatalf("the rewritten transcript must start with the marked summary row: %+v", s.Messages[0])
		}
		if s.Messages[1].Role != core.RoleAssistant || s.Messages[2].Role != core.RoleTool {
			t.Fatalf("the kept tail must stay whole: %+v", s.Messages[1:])
		}
		if evs := stripCue(fe.snapshot()); len(evs) != 1 {
			t.Fatalf("the trigger path must emit exactly one Compacted: %v", evs)
		}
	})

	t.Run("anchorless at the boundary is passthrough", func(t *testing.T) {
		s := core.NewSession()
		// system (1B -> 1) + 449 + 450 = 900 = Window - Reserve, no anchor
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 1796)})
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("q", 1800)})
		prov := &scriptedProvider{}
		pol, err := compact.New(prov, &captureFrontend{}, s, system, testRow)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		if prov.calls() != 0 {
			t.Fatalf("the boundary must not compact (provider calls = %d)", prov.calls())
		}
	})

	t.Run("anchorless one over compacts", func(t *testing.T) {
		s := core.NewSession()
		// system (1) + 449 + 451 = 901 > 900
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 1796)})
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("q", 1804)})
		prov := &scriptedProvider{turns: []scriptedTurn{{
			events: []core.Event{core.TextDelta{Text: "SUM"}, core.Done{}},
		}}}
		pol, err := compact.New(prov, &captureFrontend{}, s, system, testRow)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		if prov.calls() != 1 {
			t.Fatalf("one over the trigger must compact (provider calls = %d)", prov.calls())
		}
	})
}

// TestTriggerMathPerModelOneConfig (named): one table, one root: the
// worker row and the brain row; a transcript over the worker's trigger
// and under the brain's — the worker compacts, the brain passes through
// byte-identically. The pi shape (Reserve >= Window) cannot exist.
func TestTriggerMathPerModelOneConfig(t *testing.T) {
	worker := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
	brain := models.Model{Role: models.RoleInteractive, ID: "brain", Window: 4000, MaxTokens: 500, Reserve: 200, KeepRecent: 500}
	table, err := models.New(worker, brain)
	if err != nil {
		t.Fatalf("one table carrying both rows: %v", err)
	}

	// the shared fixture: size = anchor 800 + est(delta 800B = 200) = 1000,
	// over the worker's trigger (900), under the brain's (3800).
	fixture := func() *core.Session {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 400)})
		s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 400), ContextTokens: 800})
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 800)})
		return s
	}
	summary := []core.Event{core.TextDelta{Text: "SUM"}, core.Done{}}

	sw := fixture()
	provW := &scriptedProvider{turns: []scriptedTurn{{events: summary}}}
	polW, err := compact.New(provW, &captureFrontend{}, sw, "S", worker)
	if err != nil {
		t.Fatalf("New(worker): %v", err)
	}
	if _, err := polW.Assemble(context.Background(), sw); err != nil {
		t.Fatalf("worker Assemble: %v", err)
	}
	if provW.calls() != 1 {
		t.Fatalf("the worker must compact (calls = %d)", provW.calls())
	}

	sb := fixture()
	provB := &scriptedProvider{}
	polB, err := compact.New(provB, &captureFrontend{}, sb, "S", brain)
	if err != nil {
		t.Fatalf("New(brain): %v", err)
	}
	got, err := polB.Assemble(context.Background(), sb)
	if err != nil {
		t.Fatalf("brain Assemble: %v", err)
	}
	want, err := policy.Passthrough("S").Assemble(context.Background(), sb)
	if err != nil {
		t.Fatalf("Passthrough: %v", err)
	}
	if len(got) != len(want) || !reflect.DeepEqual(got[0], want[0]) || !reflect.DeepEqual(got[len(got)-1], want[len(want)-1]) {
		t.Fatalf("the brain must pass through byte-identically: got %d messages, want %d", len(got), len(want))
	}
	if provB.calls() != 0 {
		t.Fatalf("the brain must not compact (calls = %d)", provB.calls())
	}

	// the named pi case (2026-08-15): a global-reserve shape cannot exist.
	piShape := models.Model{Role: models.RoleInteractive, ID: "pi", Window: 100, MaxTokens: 10, Reserve: 100}
	if err := piShape.Check(); err == nil {
		t.Fatal("Reserve >= Window must be refused at construction (the pi shape)")
	}
	_ = table
}

// TestSummaryMaxTokensClamped (named): the summary request's MaxTokens is
// min(row.MaxTokens, Window - est(input)) — 3's honest budget, the
// reserve not subtracted twice; a budget <= 0 fails loud, naming the
// row's numbers.
func TestSummaryMaxTokensClamped(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 200}

	t.Run("honest budget", func(t *testing.T) {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 2000)}) // 500, the older prefix
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("q", 2000)}) // 500, the tail
		prov := &scriptedProvider{turns: []scriptedTurn{{events: []core.Event{core.TextDelta{Text: "S"}, core.Done{}}}}}
		pol, err := compact.New(prov, &captureFrontend{}, s, "", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		reqs := prov.reqs()
		if len(reqs) != 1 {
			t.Fatalf("provider calls = %d, want 1 (the summary call)", len(reqs))
		}
		// the summary input is the 3 shape: the short system role plus one
		// user message carrying the older prefix (500) as quoted transcript
		// data and the prompt's instruction. The clamp is Window -
		// est(input), bounded by MaxTokens — 3's honest budget, the reserve
		// not subtracted twice. est is the same stdlib rule as the
		// policy's: per message, bytes/4 rounded up, over the request's
		// actual messages.
		est := compact.Estimate(reqs[0].Messages)
		want := row.Window - est
		if want > row.MaxTokens {
			want = row.MaxTokens
		}
		if reqs[0].MaxTokens != want {
			t.Fatalf("MaxTokens = %d, want %d (Window %d - est(input) %d)", reqs[0].MaxTokens, want, row.Window, est)
		}
	})

	t.Run("budget at or under zero fails loud", func(t *testing.T) {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 5000)}) // 1250
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("q", 100)})  // 25, the tail
		prov := &scriptedProvider{}
		pol, err := compact.New(prov, &captureFrontend{}, s, "", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = pol.Assemble(context.Background(), s)
		if err == nil {
			t.Fatal("Assemble = ok, want the loud failure")
		}
		// the input (the 3 shape over the 1250-token prefix) does not fit
		// the 1000-token window: the refusal names the row's numbers, with
		// the input's estimate computed the policy's way — over the exact
		// message list the policy would have sent.
		older := []core.Message{{Role: core.RoleUser, Content: strings.Repeat("p", 5000)}}
		wantEst := compact.Estimate(compact.SummaryInput(older))
		msg := err.Error()
		for _, want := range []string{"local", "1000", fmt.Sprint(wantEst)} {
			if !strings.Contains(msg, want) {
				t.Fatalf("the refusal must name %q: %v", want, err)
			}
		}
	})
}

// TestRenderTranscriptRendersRolesCallsAndResults (named): one line per
// message, role prefixed — a multi-line user message keeps its lines,
// an assistant's content precedes its [calls] line, a result is its
// tool line.
func TestRenderTranscriptRendersRolesCallsAndResults(t *testing.T) {
	older := []core.Message{
		{Role: core.RoleUser, Content: "line one\nline two"},
		{Role: core.RoleAssistant, Content: "checking", ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: []byte(`{"command":"ls"}`)}}},
		{Role: core.RoleTool, ToolID: "c1", Content: "out"},
	}
	got := compact.RenderTranscript(older)
	want := "user: line one\nline two\n" +
		"assistant: checking\n" +
		"assistant: [calls bash] {\"command\":\"ls\"}\n" +
		"tool: out\n"
	if got != want {
		t.Fatalf("render = %q, want %q", got, want)
	}
}

// TestSummarySummarizesRatherThanContinues (named, decision 3): an older
// prefix whose last user message says "reply with only X" and whose last
// assistant message is a tool call must be summarized as data — the
// request is the short system role plus one user message carrying the
// prefix inside a quoted <transcript> block (the call and its result
// rendered as lines, the trap instruction as a line, not a live
// message), followed by the prompt's instruction, no tools, no live tool
// calls; and the summary describes the request and the call, never X or
// a tool call.
func TestSummarySummarizesRatherThanContinues(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1900, MaxTokens: 500, Reserve: 100, KeepRecent: 10}
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 120)})
	s.Append(core.Message{Role: core.RoleUser, Content: "Set up the build."})
	s.Append(core.Message{
		Role:      core.RoleAssistant,
		ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: []byte(`{"command":"make build"}`)}},
	})
	s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: "ok"})
	s.Append(core.Message{Role: core.RoleUser, Content: "reply with only X"})
	s.Append(core.Message{
		Role: core.RoleAssistant, ContextTokens: 1800, // the L8 anchor: size 1800 + est(tail) 1 = 1801 > 1800
		ToolCalls: []core.ToolCall{{ID: "c2", Name: "bash", Args: []byte(`{"command":"echo X"}`)}},
	})
	s.Append(core.Message{Role: core.RoleTool, ToolID: "c2", Content: "X"}) // the tail (KeepRecent 10)
	// the scripted summary plays the model's answer under the 3 shape:
	// it describes the request and the call, instead of continuing them.
	const summary = "The session set up the build: the model called bash with make build (it printed ok); the user then asked to reply with only X."
	prov := &scriptedProvider{turns: []scriptedTurn{{
		events: []core.Event{core.TextDelta{Text: summary}, core.Done{Usage: core.Usage{Prompt: 10, Completion: 5}}},
	}}}
	fe := &captureFrontend{}
	pol, err := compact.New(prov, fe, s, "S", row)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := pol.Assemble(context.Background(), s); err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	reqs := prov.reqs()
	if len(reqs) != 1 {
		t.Fatalf("provider calls = %d, want 1 (the summary call)", len(reqs))
	}
	msgs := reqs[0].Messages
	if len(msgs) != 2 {
		t.Fatalf("the summary request = %d messages, want exactly two (the role; the transcript plus the instruction)", len(msgs))
	}
	if msgs[0].Role != core.RoleSystem || msgs[0].Content != compact.SummarySystem {
		t.Fatalf("message 0 = %+v, want the short system role %q", msgs[0], compact.SummarySystem)
	}
	if msgs[1].Role != core.RoleUser {
		t.Fatalf("message 1 = role %s, want user (one message carrying the transcript)", msgs[1].Role)
	}
	c := msgs[1].Content
	if !strings.HasPrefix(c, "<transcript>\n") {
		t.Fatalf("the user message must open the quoted block, got %q...", c[:30])
	}
	for _, want := range []string{
		"user: Set up the build.",
		"assistant: [calls bash] {\"command\":\"make build\"}",
		"tool: ok",
		"user: reply with only X", // the trap instruction, as a quoted line
	} {
		if !strings.Contains(c, want) {
			t.Fatalf("the quoted transcript must render %q", want)
		}
	}
	// the prompt's instruction follows the closing tag — never before
	// it — and the kept tail is not in the block at all.
	prompt, perr := os.ReadFile("summary_prompt.txt")
	if perr != nil {
		t.Fatalf("read the prompt file: %v", perr)
	}
	if !strings.Contains(c, string(prompt)) {
		t.Fatal("the user message must carry the summary_prompt.txt instruction")
	}
	tag := strings.Index(c, "</transcript>")
	if tag < 0 {
		t.Fatal("the quoted block must be closed")
	}
	if p := strings.Index(c, string(prompt[:40])); p < 0 || p < tag {
		t.Fatalf("the instruction must follow the closing tag (tag %d, prompt %d)", tag, p)
	}
	if strings.Contains(c[:tag], "echo X") {
		t.Fatalf("the quoted block must not carry the kept tail: %q", c[:tag])
	}
	for i, m := range msgs {
		if len(m.ToolCalls) != 0 {
			t.Fatalf("message %d carries live tool calls %+v — the prefix is data, not a conversation", i, m.ToolCalls)
		}
		if m.Content == "reply with only X" {
			t.Fatalf("message %d is the trap instruction as a live message — it must be a quoted line", i)
		}
	}
	if reqs[0].ReasoningEffort != "medium" {
		t.Fatalf("ReasoningEffort = %q, want medium (the one call whose thinking nobody reads)", reqs[0].ReasoningEffort)
	}

	// the rewrite: [summary row] + the kept tail, whole.
	if len(s.Messages) != 3 {
		t.Fatalf("transcript = %d messages, want the summary + the kept tail", len(s.Messages))
	}
	if s.Messages[0].Role != core.RoleUser || s.Messages[0].Content != compact.SummaryMarker+summary {
		t.Fatalf("the rewritten transcript must start with the marked summary row: %+v", s.Messages[0])
	}
	if s.Messages[1].ToolCalls[0].ID != "c2" || s.Messages[2].Role != core.RoleTool {
		t.Fatalf("the kept tail must be the pair, whole: %+v", s.Messages[1:])
	}
	// the summary describes the request and the call; it is never X and
	// never a tool call.
	if s.Messages[0].Content == compact.SummaryMarker+"X" {
		t.Fatal("the summary is the trap instruction's answer")
	}
	for _, want := range []string{"reply with only X", "bash", "make build"} {
		if !strings.Contains(s.Messages[0].Content, want) {
			t.Fatalf("the summary must describe %q: %q", want, s.Messages[0].Content)
		}
	}
	if strings.Contains(s.Messages[0].Content, `{"command"`) {
		t.Fatalf("the summary must not be a tool call: %q", s.Messages[0].Content)
	}
	evs := stripCue(fe.snapshot())
	if len(evs) != 1 {
		t.Fatalf("frontend events = %v, want exactly one Compacted", evs)
	}
	cEv, ok := evs[0].(core.Compacted)
	if !ok {
		t.Fatalf("event 0 = %T, want Compacted", evs[0])
	}
	if cEv.Kept != 7 {
		t.Fatalf("Kept = %d, want the tail's estimate (7)", cEv.Kept)
	}
}

// TestCompactedEventBeforeTheNextCall (named): the trigger path emits
// Compacted before the next model call's events, and the fields are
// right: Summary is the transcript's summary content, Dropped/Kept are
// the calibrated estimates, Usage is the summary call's reported usage.
func TestCompactedEventBeforeTheNextCall(t *testing.T) {
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 2000)}) // 500, the older prefix
	s.Append(core.Message{
		Role: core.RoleAssistant, Content: strings.Repeat("a", 200),
		ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: []byte(`{}`)}}, // 50
	})
	s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 2000)}) // 500, the tail

	prov := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{core.TextDelta{Text: "THE SUMMARY"}, core.Done{Usage: core.Usage{Prompt: 777, Completion: 33}}}},
		// the next model call's events, replayed through the same frontend
		{events: []core.Event{core.TextDelta{Text: "next"}, core.Done{}}},
	}}
	fe := &captureFrontend{}
	pol, err := compact.New(prov, fe, s, "S", testRow)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := pol.Assemble(context.Background(), s); err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	// the loop streams the next call through the frontend it holds
	next, err := prov.Stream(context.Background(), core.Request{Messages: []core.Message{{Role: core.RoleUser, Content: "x"}}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for ev := range next {
		fe.Notify(ev)
	}

	evs := stripCue(fe.snapshot())
	if len(evs) != 3 {
		t.Fatalf("frontend events = %v, want Compacted then the call's two events", evs)
	}
	c, ok := evs[0].(core.Compacted)
	if !ok {
		t.Fatalf("event 0 = %T, want Compacted (before the next call's events)", evs[0])
	}
	if c.Summary != s.Messages[0].Content {
		t.Fatalf("Summary = %q, want the transcript's summary row %q", c.Summary, s.Messages[0].Content)
	}
	if c.Summary != compact.SummaryMarker+"THE SUMMARY" {
		t.Fatalf("Summary = %q, want the marker plus the body", c.Summary)
	}
	if c.Dropped != 500 {
		t.Fatalf("Dropped = %d, want the older prefix's estimate (500)", c.Dropped)
	}
	if c.Kept != 552 {
		t.Fatalf("Kept = %d, want the tail's estimate (52 + 500)", c.Kept)
	}
	if c.Usage.Prompt != 777 || c.Usage.Completion != 33 {
		t.Fatalf("Usage = %+v, want the summary call's reported usage", c.Usage)
	}
}

// TestSecondCompactionFoldsTheFirst (named): the second compact's older
// prefix contains the first summary row; the transcript after equals
// [new summary] + tail; the seam is called with each new body.
func TestSecondCompactionFoldsTheFirst(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p1", 1000)}) // 500
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p2", 1000)}) // 500, the tail

	var bodies []string
	reflect := func(_ context.Context, summary string) error { bodies = append(bodies, summary); return nil }

	prov := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{core.TextDelta{Text: "SUM1"}, core.Done{}}},
		{events: []core.Event{core.TextDelta{Text: "SUM2"}, core.Done{}}},
	}}
	fe := &captureFrontend{}
	pol, err := compact.New(prov, fe, s, "S", row, compact.WithAutoReflect(reflect))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := pol.Assemble(context.Background(), s); err != nil {
		t.Fatalf("first Assemble: %v", err)
	}
	if len(s.Messages) != 2 || s.Messages[0].Content != compact.SummaryMarker+"SUM1" {
		t.Fatalf("after the first compact the transcript = %+v, want [SUM1 row, the tail]", s.Messages)
	}

	// the session grows past the trigger again. The second-growth tail must
	// stay small enough that the fold's older prefix (decision 3's summary
	// input) still fits the summary window: p3 is the older fold, p4 the
	// tail (400 tokens).
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p3", 400)})  // 100, the older fold
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p4", 1600)}) // 400, the tail
	if _, err := pol.Assemble(context.Background(), s); err != nil {
		t.Fatalf("second Assemble: %v", err)
	}

	// the fold: the second summary's input carried the first summary row
	reqs := prov.reqs()
	if len(reqs) != 2 {
		t.Fatalf("summary calls = %d, want 2", len(reqs))
	}
	folded := false
	for _, m := range reqs[1].Messages {
		if strings.Contains(m.Content, "SUM1") {
			folded = true
		}
	}
	if !folded {
		t.Fatalf("the second compact's input must carry the first summary row: %+v", reqs[1].Messages)
	}
	if len(s.Messages) != 2 || s.Messages[0].Content != compact.SummaryMarker+"SUM2" {
		t.Fatalf("after the second compact the transcript = %+v, want [SUM2 row, the tail]", s.Messages)
	}
	if len(bodies) != 2 || bodies[0] != "SUM1" || bodies[1] != "SUM2" {
		t.Fatalf("the reflection seam = %v, want each new body in order", bodies)
	}
}

// TestAutoReflectLandsDedupesAndNeverFails (named): a real rem store —
// the memory row (kind reflection, importance 0.2, cwd scope, source
// "session compaction"); a store failure (a closed db) leaves Assemble
// successful; the absent seam skips the call and changes nothing else.
func TestAutoReflectLandsDedupesAndNeverFails(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 200}
	cwd := t.TempDir()

	t.Run("the reflection lands with pane's session_compact shape", func(t *testing.T) {
		rdb, _, err := store.Open(filepath.Join(cwd, "rem.sqlite"), remstore.Statements(), remstore.SchemaVersion)
		if err != nil {
			t.Fatalf("open the rem store: %v", err)
		}
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p1", 1000)}) // 500
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p2", 1000)}) // 500, the tail
		prov := &scriptedProvider{turns: []scriptedTurn{{events: []core.Event{core.TextDelta{Text: "REAL SUMMARY"}, core.Done{}}}}}
		pol, err := compact.New(prov, &captureFrontend{}, s, "S", row, compact.WithAutoReflect(func(ctx context.Context, summary string) error {
			_, err := remstore.AutoReflect(ctx, rdb, cwd, summary)
			return err
		}))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		var (
			kind       string
			source     sql.NullString
			importance float64
			scope      string
		)
		if err := rdb.DB.QueryRow(`SELECT kind, source, importance, scope FROM memories`).Scan(&kind, &source, &importance, &scope); err != nil {
			t.Fatalf("the memory row: %v", err)
		}
		// the store keys project scope as shortHash(cwd) (sha1[:12], rem),
		// not the raw path — the same shaping the rem tests assert.
		d := sha1.Sum([]byte(cwd))
		wantScope := hex.EncodeToString(d[:])[:12]
		if kind != "reflection" || source.String != "session compaction" || importance != 0.2 || scope != wantScope {
			t.Fatalf("memory row = kind %q source %q importance %g scope %q, want reflection / session compaction / 0.2 / the cwd (scope %q)",
				kind, source.String, importance, scope, wantScope)
		}
	})

	t.Run("a replayed body is inert", func(t *testing.T) {
		rdb, _, err := store.Open(filepath.Join(t.TempDir(), "rem.sqlite"), remstore.Statements(), remstore.SchemaVersion)
		if err != nil {
			t.Fatalf("open the rem store: %v", err)
		}
		cwd := t.TempDir()
		reflect := func(ctx context.Context, summary string) error {
			_, err := remstore.AutoReflect(ctx, rdb, cwd, summary)
			return err
		}
		newSession := func() *core.Session {
			s := core.NewSession()
			s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p1", 1000)})
			s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p2", 1000)})
			return s
		}
		for i := 0; i < 2; i++ {
			s := newSession()
			prov := &scriptedProvider{turns: []scriptedTurn{{events: []core.Event{core.TextDelta{Text: "SAME BODY"}, core.Done{}}}}}
			pol, err := compact.New(prov, &captureFrontend{}, s, "S", row, compact.WithAutoReflect(reflect))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := pol.Assemble(context.Background(), s); err != nil {
				t.Fatalf("Assemble %d: %v", i, err)
			}
		}
		var n int
		if err := rdb.DB.QueryRow(`SELECT count(*) FROM memories`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("memories = %d, want 1 (the dedup makes a replayed body inert)", n)
		}
	})

	t.Run("a store failure leaves Assemble successful", func(t *testing.T) {
		rdb, _, err := store.Open(filepath.Join(t.TempDir(), "rem.sqlite"), remstore.Statements(), remstore.SchemaVersion)
		if err != nil {
			t.Fatalf("open the rem store: %v", err)
		}
		if err := rdb.DB.Close(); err != nil {
			t.Fatal(err)
		}
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p1", 1000)})
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p2", 1000)})
		prov := &scriptedProvider{turns: []scriptedTurn{{events: []core.Event{core.TextDelta{Text: "S"}, core.Done{}}}}}
		fe := &captureFrontend{}
		pol, err := compact.New(prov, fe, s, "S", row, compact.WithAutoReflect(func(ctx context.Context, summary string) error {
			_, err := remstore.AutoReflect(ctx, rdb, cwd, summary)
			return err
		}))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("a store failure must never fail the turn: %v", err)
		}
		if len(s.Messages) != 2 || s.Messages[0].Content != compact.SummaryMarker+"S" {
			t.Fatalf("the compact must have happened: %+v", s.Messages)
		}
		if evs := stripCue(fe.snapshot()); len(evs) != 1 {
			t.Fatalf("the event must have been emitted: %v", evs)
		}
	})

	t.Run("the absent seam skips the call and changes nothing else", func(t *testing.T) {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p1", 1000)})
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p2", 1000)})
		prov := &scriptedProvider{turns: []scriptedTurn{{events: []core.Event{core.TextDelta{Text: "S"}, core.Done{}}}}}
		fe := &captureFrontend{}
		pol, err := compact.New(prov, fe, s, "S", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		// the seam is absent: the compact and the event still happen
		if len(s.Messages) != 2 || s.Messages[0].Content != compact.SummaryMarker+"S" {
			t.Fatalf("the compact must happen without the seam: %+v", s.Messages)
		}
		if evs := stripCue(fe.snapshot()); len(evs) != 1 {
			t.Fatalf("the event must happen without the seam: %v", evs)
		}
	})
}

// TestSummaryEffortIsTheRow (SPEC_CONFIG 4, named): the summary call's
// reasoning effort is the row's Effort — the one call whose thinking
// nobody reads takes the row's budget where the operator set one; the
// policy keeps "medium" as the field's default (the 0.2.0 behavior, now
// the field's default).
func TestSummaryEffortIsTheRow(t *testing.T) {
	compactFixture := func(row models.Model) (*scriptedProvider, *core.Session) {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 2000)}) // 500, the older prefix
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("q", 2000)}) // 500, the tail
		prov := &scriptedProvider{turns: []scriptedTurn{{events: []core.Event{core.TextDelta{Text: "S"}, core.Done{}}}}}
		return prov, s
	}
	base := models.Model{ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 200, Role: models.RoleInteractive}

	t.Run("the row's effort rides the summary call", func(t *testing.T) {
		row := base
		row.Effort = "low"
		prov, s := compactFixture(row)
		pol, err := compact.New(prov, &captureFrontend{}, s, "", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		reqs := prov.reqs()
		if len(reqs) != 1 {
			t.Fatalf("provider calls = %d, want 1 (the summary call)", len(reqs))
		}
		if reqs[0].ReasoningEffort != "low" {
			t.Fatalf("ReasoningEffort = %q, want the row's low", reqs[0].ReasoningEffort)
		}
	})
	t.Run("an empty field keeps the policy's medium", func(t *testing.T) {
		row := base // Effort ""
		prov, s := compactFixture(row)
		pol, err := compact.New(prov, &captureFrontend{}, s, "", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		reqs := prov.reqs()
		if len(reqs) != 1 {
			t.Fatalf("provider calls = %d, want 1 (the summary call)", len(reqs))
		}
		if reqs[0].ReasoningEffort != "medium" {
			t.Fatalf("ReasoningEffort = %q, want the field's default medium", reqs[0].ReasoningEffort)
		}
	})
}

// TestCompactingCueOrder (SPEC_COMPACT 5, amended): the frontend hears
// Compacting before the summary call runs — so the operator sees a
// loader, not a hang, through a minutes-long deep-context prefill —
// exactly once per compaction, and never on the passthrough.
func TestCompactingCueOrder(t *testing.T) {
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 400)})
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 200), ContextTokens: 801})
	s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 400)})
	prov := &scriptedProvider{turns: []scriptedTurn{{
		events: []core.Event{core.TextDelta{Text: "SUMMARY"}, core.Done{}},
	}}}
	fe := &captureFrontend{}
	pol, err := compact.New(prov, fe, s, "S", testRow)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := pol.Assemble(context.Background(), s); err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	evs := fe.snapshot()
	if len(evs) != 2 {
		t.Fatalf("events = %v, want the cue then Compacted", evs)
	}
	if _, ok := evs[0].(core.Compacting); !ok {
		t.Fatalf("event 0 = %T, want the Compacting cue first", evs[0])
	}
	if _, ok := evs[1].(core.Compacted); !ok {
		t.Fatalf("event 1 = %T, want Compacted after the cue", evs[1])
	}
}

// TestOversizedOlderCompactsTheOldestSliceThatFits (SPEC_COMPACT 3,
// amended 2026-08-21): an older prefix whose summary input does not fit
// the window is cut to the oldest slice that does — one call — and the
// remainder rides ahead of the tail, uncompacted, to fold on a later
// pass. Before the amendment this was the loud failure that stuck a
// session until /new.
func TestOversizedOlderCompactsTheOldestSliceThatFits(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 1200)})      // 300
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 1200)}) // 300
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("q", 1200)})      // 300
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("b", 1200)}) // 300
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("t", 100)})       // 25, the tail
	// older = the first four (est 1200 + the prompt's ~200) does not fit
	// a 1000 window; the first two (~800) leave the floor (25).
	prov := &scriptedProvider{turns: []scriptedTurn{summaryTurn("S1", core.Usage{Prompt: 800, Completion: 20})}}
	fe := &captureFrontend{}
	pol, err := compact.New(prov, fe, s, "", row)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := pol.Assemble(context.Background(), s)
	if err != nil {
		t.Fatalf("Assemble must compact the slice, not fail: %v", err)
	}
	if prov.calls() != 1 {
		t.Fatalf("one summary call, got %d", prov.calls())
	}
	sent := prov.reqs()[0].Messages[1].Content
	if !strings.Contains(sent, "pppp") || !strings.Contains(sent, "aaaa") {
		t.Fatalf("the slice must hold the oldest messages, got %d bytes", len(sent))
	}
	if strings.Contains(sent, "qqqq") || strings.Contains(sent, "bbbb") {
		t.Fatal("the remainder must not be in the summary input")
	}
	// the transcript: marker + the remainder + the tail
	if len(s.Messages) != 4 || !strings.HasPrefix(s.Messages[0].Content, compact.SummaryMarker) ||
		!strings.HasPrefix(s.Messages[1].Content, "qqqq") || !strings.HasPrefix(s.Messages[2].Content, "bbbb") ||
		!strings.HasPrefix(s.Messages[3].Content, "tttt") {
		t.Fatalf("transcript = marker + remainder + tail, got %d messages", len(s.Messages))
	}
	if len(out) != len(s.Messages) {
		t.Fatalf("assemble returns the rewritten transcript (no system): %d vs %d", len(out), len(s.Messages))
	}
	var ev core.Compacted
	found := false
	for _, e := range fe.events {
		if c, ok := e.(core.Compacted); ok {
			ev, found = c, true
		}
	}
	if !found {
		t.Fatal("the Compacted event must be delivered")
	}
	if ev.Dropped < 590 || ev.Dropped > 610 || ev.Kept < 615 || ev.Kept > 635 {
		t.Fatalf("Dropped is the slice (~600) and Kept is remainder plus tail (~625), got %d / %d", ev.Dropped, ev.Kept)
	}
}

// A slice never leads its remainder with a tool result whose call was
// cut away: the cut moves back to the call, as split's does.
func TestOversizedOlderSliceRespectsTheCallBoundary(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 1200)})                                                                                             // 300
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 400), ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: []byte(`{"command":"ls"}`)}}}) // ~105
	s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 2000)})                                                                               // 500, the result
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("b", 1200)})                                                                                        // 300
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("t", 100)})                                                                                              // 25, the tail
	// by size the slice would be [p, a-with-call] (~600 + prompt) and the
	// remainder would lead with c1's result; the cut moves back to the
	// call, so the slice is [p] alone.
	prov := &scriptedProvider{turns: []scriptedTurn{summaryTurn("S1", core.Usage{Prompt: 500, Completion: 20})}}
	pol, err := compact.New(prov, &captureFrontend{}, s, "", row)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := pol.Assemble(context.Background(), s); err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	sent := prov.reqs()[0].Messages[1].Content
	if !strings.Contains(sent, "pppp") || strings.Contains(sent, "[calls bash]") || strings.Contains(sent, "rrrr") {
		t.Fatalf("the slice must stop before the call whose result would lead the remainder, got %d bytes", len(sent))
	}
	if len(s.Messages) != 5 || len(s.Messages[1].ToolCalls) != 1 || s.Messages[2].ToolID != "c1" {
		t.Fatalf("the call and its result must stay together in the remainder, got %d messages", len(s.Messages))
	}
}
