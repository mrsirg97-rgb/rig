package compact_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/loop"
	"github.com/mrsirg97-rgb/rig/models"
	compact "github.com/mrsirg97-rgb/rig/policy/compact"
)

const contextFault = "openai: 400: prompt is too long: context length exceeded"

func summaryTurn(body string, usage core.Usage) scriptedTurn {
	return scriptedTurn{events: []core.Event{core.TextDelta{Text: body}, core.Done{Usage: usage}}}
}

func healthyTurn(text string, usage core.Usage) scriptedTurn {
	return scriptedTurn{events: []core.Event{core.TextDelta{Text: text}, core.Done{Usage: usage}}}
}

// TestOverflowRecoversOnce (SPEC_COMPACT, named): a pre-stream
// context-length fault, then a healthy stream: the frontend's order is
// Compacted, TextDelta*, Done; the first Fault never surfaces; the
// second request's messages equal system + [summary row] + tail; the
// session transcript is rewritten; AutoReflect is called.
func TestOverflowRecoversOnce(t *testing.T) {
	t.Run("pre-stream fault is swallowed and recovered", func(t *testing.T) {
		s := compactFixture()
		prov := &scriptedProvider{turns: []scriptedTurn{
			{events: []core.Event{core.Fault{Err: errors.New(contextFault)}}},
			summaryTurn("SUMMARY", core.Usage{Prompt: 77, Completion: 8}),
			healthyTurn("retry answer", core.Usage{Prompt: 100, Completion: 5}),
		}}
		fe := &captureFrontend{}
		var bodies []string
		pol, err := compact.New(prov, fe, s, "S", overflowRow, compact.WithAutoReflect(func(_ context.Context, b string) error {
			bodies = append(bodies, b)
			return nil
		}))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		// the loop appends the user line before the call: the transcript
		// has grown past the construction baseline, so a recovery is owed
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("n", 400)})
		dec := compact.Decorator(prov, pol)
		tools := []core.ToolSpec{{Name: "bash", Description: "runs", Schema: []byte(`{}`)}}
		req := core.Request{Messages: append([]core.Message{{Role: core.RoleSystem, Content: "S"}}, s.Messages...), Tools: tools}

		out, err := dec.Stream(context.Background(), req)
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for ev := range out {
			fe.Notify(ev)
		}

		evs := fe.snapshot()
		if len(evs) != 3 {
			t.Fatalf("frontend order = %v, want Compacted, TextDelta, Done", evs)
		}
		if _, ok := evs[0].(core.Compacted); !ok {
			t.Fatalf("event 0 = %T, want Compacted (the fault never surfaces)", evs[0])
		}
		if td, ok := evs[1].(core.TextDelta); !ok || td.Text != "retry answer" {
			t.Fatalf("event 1 = %v, want the retry's text", evs[1])
		}
		if _, ok := evs[2].(core.Done); !ok {
			t.Fatalf("event 2 = %T, want Done", evs[2])
		}

		// the second request's messages equal system + [summary row] + the
		// kept tail (the appended user line, the last message).
		reqs := prov.reqs()
		if len(reqs) != 3 {
			t.Fatalf("provider calls = %d, want 3 (main, summary, retry)", len(reqs))
		}
		retry := reqs[2]
		if len(retry.Messages) != 3 {
			t.Fatalf("retry = %d messages, want system + summary + the kept tail (1)", len(retry.Messages))
		}
		if retry.Messages[0].Content != "S" || retry.Messages[0].Role != core.RoleSystem {
			t.Fatalf("retry message 0 = %+v, want the system", retry.Messages[0])
		}
		wantRow := core.Message{Role: core.RoleUser, Content: compact.SummaryMarker + "SUMMARY"}
		if !reflect.DeepEqual(retry.Messages[1], wantRow) {
			t.Fatalf("retry message 1 = %+v, want the summary row", retry.Messages[1])
		}
		if retry.Messages[2].Role != core.RoleUser || retry.Messages[2].Content != strings.Repeat("n", 400) {
			t.Fatalf("the retry must carry the kept tail: %+v", retry.Messages[2])
		}
		if len(retry.Tools) != 1 || retry.Tools[0].Name != "bash" {
			t.Fatalf("the retry must re-issue the same request shape (tools): %+v", retry.Tools)
		}

		// the session transcript is rewritten
		if len(s.Messages) != 2 || s.Messages[0].Content != compact.SummaryMarker+"SUMMARY" {
			t.Fatalf("session = %+v, want [summary, the kept tail]", s.Messages)
		}
		if len(bodies) != 1 || bodies[0] != "SUMMARY" {
			t.Fatalf("AutoReflect = %v, want the summary", bodies)
		}
	})

	t.Run("a fault after deltas leaves the partial and follows the retry", func(t *testing.T) {
		// decision 7's named shape: the partial is not retracted — the
		// model started, the context compacted, the model continued.
		s := compactFixture()
		prov := &scriptedProvider{turns: []scriptedTurn{
			{events: []core.Event{core.TextDelta{Text: "partial "}, core.Fault{Err: errors.New(contextFault)}}},
			summaryTurn("SUM", core.Usage{Prompt: 10, Completion: 1}),
			healthyTurn("done", core.Usage{Prompt: 20, Completion: 2}),
		}}
		fe := &captureFrontend{}
		pol, err := compact.New(prov, fe, s, "S", overflowRow)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("n", 400)})
		dec := compact.Decorator(prov, pol)
		req := core.Request{Messages: append([]core.Message{{Role: core.RoleSystem, Content: "S"}}, s.Messages...)}
		out, err := dec.Stream(context.Background(), req)
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for ev := range out {
			fe.Notify(ev)
		}
		var order []string
		for _, ev := range fe.snapshot() {
			switch e := ev.(type) {
			case core.TextDelta:
				order = append(order, "text:"+e.Text)
			case core.Compacted:
				order = append(order, "compacted")
			case core.Done:
				order = append(order, "done")
			default:
				order = append(order, "unexpected:"+stringify(ev))
			}
		}
		want := "text:partial ,compacted,text:done,done"
		if strings.Join(order, ",") != want {
			t.Fatalf("the partial must stay, then the compaction, then the retry: got %q, want %q", strings.Join(order, ","), want)
		}
	})
}

// TestOverflowRecoversOnceThenSurfaces (named): two classifiable faults:
// the second surfaces (the Fault reaches the frontend); a third main call
// never happens; after a new user message (the transcript grew) one more
// recovery is owed and happens.
func TestOverflowRecoversOnceThenSurfaces(t *testing.T) {
	s := compactFixture()
	prov := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{core.Fault{Err: errors.New(contextFault)}}}, // main 1
		summaryTurn("SUM1", core.Usage{Prompt: 10, Completion: 1}),
		{events: []core.Event{core.Fault{Err: errors.New(contextFault)}}}, // retry 1: the second classifiable fault
		{events: []core.Event{core.Fault{Err: errors.New(contextFault)}}}, // main 2 (after growth)
		summaryTurn("SUM2", core.Usage{Prompt: 10, Completion: 1}),
		healthyTurn("ANSWER", core.Usage{Prompt: 20, Completion: 2}), // retry 2
	}}
	fe := &captureFrontend{}
	pol, err := compact.New(prov, fe, s, "S", overflowRow)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dec := compact.Decorator(prov, pol)
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("n", 400)})
	req := core.Request{Messages: append([]core.Message{{Role: core.RoleSystem, Content: "S"}}, s.Messages...)}

	stream := func() {
		out, err := dec.Stream(context.Background(), req)
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for ev := range out {
			fe.Notify(ev)
		}
	}
	stream()
	evs := fe.snapshot()
	if len(evs) != 2 {
		t.Fatalf("first stream = %v, want Compacted then the surfaced Fault", evs)
	}
	if _, ok := evs[0].(core.Compacted); !ok {
		t.Fatalf("event 0 = %T, want Compacted (the first recovery)", evs[0])
	}
	if f, ok := evs[1].(core.Fault); !ok || !strings.Contains(f.Err.Error(), "context length") {
		t.Fatalf("event 1 = %v, want the second fault surfaced", evs[1])
	}
	// a third main call never happened: main 1, summary 1, retry 1
	if prov.calls() != 3 {
		t.Fatalf("provider calls = %d, want 3 (no third main call)", prov.calls())
	}

	// a new user message: the transcript grew — one more recovery is owed
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("n", 2000)})
	stream()
	evs = fe.snapshot()[2:]
	if len(evs) != 3 {
		t.Fatalf("second stream = %v, want Compacted, TextDelta, Done", evs)
	}
	if _, ok := evs[0].(core.Compacted); !ok {
		t.Fatalf("event 2 = %T, want the owed recovery", evs[0])
	}
	if td, ok := evs[1].(core.TextDelta); !ok || td.Text != "ANSWER" {
		t.Fatalf("event 3 = %v, want the retry's answer", evs[1])
	}

	// the fold: the second summary's input carried the first summary row
	reqs := prov.reqs()
	folded := false
	for _, m := range reqs[4].Messages {
		if strings.Contains(m.Content, "SUM1") {
			folded = true
		}
	}
	if !folded {
		t.Fatalf("the second compact must fold the first summary row: %+v", reqs[4].Messages)
	}
}

// TestOverflowClassifier (named): the wordlist's positives are recovered;
// a timeout and a non-context fault are not.
func TestOverflowClassifier(t *testing.T) {
	positives := []string{
		"context length exceeded",
		"maximum context length",
		"context_length too small",
		"context window exceeded",
		"max context size",
		"prompt is too long",
		"prompt too long",
		"too many tokens in request",
		"exceeds the maximum allowed length",
		"CONTEXT LENGTH EXCEEDED", // case-folded
	}
	for _, phrase := range positives {
		t.Run(phrase, func(t *testing.T) {
			s := compactFixture()
			prov := &scriptedProvider{turns: []scriptedTurn{
				{events: []core.Event{core.Fault{Err: errors.New(phrase)}}},
				summaryTurn("S", core.Usage{Prompt: 1, Completion: 1}),
				healthyTurn("ok", core.Usage{Prompt: 1, Completion: 1}),
			}}
			fe := &captureFrontend{}
			pol, err := compact.New(prov, fe, s, "S", overflowRow)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("n", 400)})
			dec := compact.Decorator(prov, pol)
			out, err := dec.Stream(context.Background(), core.Request{Messages: s.Messages})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			for ev := range out {
				fe.Notify(ev)
			}
			evs := fe.snapshot()
			if len(evs) != 3 {
				t.Fatalf("%q: frontend = %v, want Compacted, TextDelta, Done", phrase, evs)
			}
			if _, ok := evs[0].(core.Compacted); !ok {
				t.Fatalf("%q: must be recovered (event 0 = %T)", phrase, evs[0])
			}
		})
	}
	negatives := []string{
		"context deadline exceeded",
		"connection reset by peer",
		"model not found",
	}
	for _, phrase := range negatives {
		t.Run(phrase, func(t *testing.T) {
			s := compactFixture()
			prov := &scriptedProvider{turns: []scriptedTurn{
				{events: []core.Event{core.Fault{Err: errors.New(phrase)}}},
			}}
			fe := &captureFrontend{}
			pol, err := compact.New(prov, fe, s, "S", overflowRow)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			dec := compact.Decorator(prov, pol)
			out, err := dec.Stream(context.Background(), core.Request{Messages: s.Messages})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			for ev := range out {
				fe.Notify(ev)
			}
			evs := fe.snapshot()
			if len(evs) != 1 {
				t.Fatalf("%q: frontend = %v, want only the fault", phrase, evs)
			}
			if _, ok := evs[0].(core.Fault); !ok {
				t.Fatalf("%q: must surface (event 0 = %T)", phrase, evs[0])
			}
			if prov.calls() != 1 {
				t.Fatalf("%q: not a context fault: no retry (calls = %d)", phrase, prov.calls())
			}
		})
	}
}

// TestCalibrationShiftsTheTrigger (named, decision 4's anchor shape): a
// scripted Done reporting anchor + 2*estimate(delta) doubles only the
// delta in the next trigger decision; a reported 0.5x is the inverse;
// ratios outside [0.5, 4.0] clamp; no anchor leaves the factor at 1.0; a
// large tool spec keeps the factor at the delta ratio (reported - anchor
// excludes the spec).
func TestCalibrationShiftsTheTrigger(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
	const anchor = 500
	// an anchored transcript with a delta of est 100 after the anchor
	base := func() *core.Session {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 400)}) // 100
		s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 400), ContextTokens: anchor})
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 400)}) // 100, the delta
		return s
	}

	t.Run("a 2x report doubles only the delta", func(t *testing.T) {
		// a wider window than the shared row: the 3 shape's summary input
		// (the quoted transcript plus the prompt) must still fit the
		// window at the doubled factor.
		row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1050, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
		s := base()
		prov := &scriptedProvider{turns: []scriptedTurn{
			{events: []core.Event{core.Done{Usage: core.Usage{Prompt: anchor + 2*100, Completion: 10}}}},
		}}
		pol, err := compact.New(prov, &captureFrontend{}, s, "S", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		dec := compact.Decorator(prov, pol)
		out, err := dec.Stream(context.Background(), core.Request{Messages: append([]core.Message{{Role: core.RoleSystem, Content: "S"}}, s.Messages...)})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for range out {
		}
		// grow the delta to est 250: raw (factor 1) size = 750 <= 950
		// (under the raw trigger); calibrated (factor 2) = 1000 > 950.
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c2", Content: strings.Repeat("r", 600)}) // +150
		prov.turns = append(prov.turns, summaryTurn("S2", core.Usage{Prompt: 5, Completion: 5}))
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		if prov.calls() != 2 {
			t.Fatalf("the corrected estimate must compact (calls = %d, want the summary call)", prov.calls())
		}
	})

	t.Run("a 0.5x report is the inverse", func(t *testing.T) {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 400)})
		s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 400), ContextTokens: anchor})
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 1800)}) // 450, the delta
		// raw size = 500 + 450 = 950 > 900 (would compact at factor 1), and
		// the main call 951 fits the window (left >= the clamp's min); a
		// 0.5x report gives calibrated size = 500 + 225 = 725 <= 900:
		// passthrough.
		prov := &scriptedProvider{turns: []scriptedTurn{
			{events: []core.Event{core.Done{Usage: core.Usage{Prompt: anchor + 225, Completion: 5}}}},
		}}
		pol, err := compact.New(prov, &captureFrontend{}, s, "S", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		dec := compact.Decorator(prov, pol)
		out, err := dec.Stream(context.Background(), core.Request{Messages: append([]core.Message{{Role: core.RoleSystem, Content: "S"}}, s.Messages...)})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for range out {
		}
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		if prov.calls() != 1 {
			t.Fatalf("the inverse must pass through (calls = %d, want none)", prov.calls())
		}
	})

	t.Run("a 10x report clamps to 4", func(t *testing.T) {
		s := base()
		prov := &scriptedProvider{turns: []scriptedTurn{
			{events: []core.Event{core.Done{Usage: core.Usage{Prompt: anchor + 10*100, Completion: 1}}}},
		}}
		pol, err := compact.New(prov, &captureFrontend{}, s, "S", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		dec := compact.Decorator(prov, pol)
		out, err := dec.Stream(context.Background(), core.Request{Messages: append([]core.Message{{Role: core.RoleSystem, Content: "S"}}, s.Messages...)})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for range out {
		}
		// delta est 100: clamped factor 4 -> size 900 == the trigger
		// (passthrough); unclamped 10 would give 1500 (compact).
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		if prov.calls() != 1 {
			t.Fatalf("the 4x clamp must hold at the boundary (calls = %d, want none)", prov.calls())
		}
	})

	t.Run("a 0.1x report clamps to 0.5", func(t *testing.T) {
		s := base() // delta 100, fits the window
		prov := &scriptedProvider{turns: []scriptedTurn{
			{events: []core.Event{core.Done{Usage: core.Usage{Prompt: anchor + 10, Completion: 1}}}},
			summaryTurn("S01", core.Usage{Prompt: 5, Completion: 5}),
		}}
		pol, err := compact.New(prov, &captureFrontend{}, s, "S", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		dec := compact.Decorator(prov, pol)
		// calibrate on the fitting main call: reported-anchor = 10 on a
		// delta of 100 -> ratio 0.1, clamped to 0.5.
		out, err := dec.Stream(context.Background(), core.Request{Messages: append([]core.Message{{Role: core.RoleSystem, Content: "S"}}, s.Messages...)})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for range out {
		}
		// grow the delta to 850: clamped 0.5 -> size 500 + 425 = 925 >
		// 900 (still compacts); unclamped 0.1 -> 585 (passthrough) — the
		// clamp is what keeps it compacting.
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c2", Content: strings.Repeat("r", 3000)}) // +750
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		if prov.calls() != 2 {
			t.Fatalf("the 0.5 clamp must keep it compacting (calls = %d, want the summary call)", prov.calls())
		}
	})

	t.Run("no anchor leaves the factor at 1.0", func(t *testing.T) {
		// system (1) + 449 + 450 = 900 == the trigger: passthrough at
		// factor 1.0. A whole-request ratio of 4 (if learned) would give
		// 3600 and compact — the factor must not move.
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 1796)}) // 449
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("q", 1800)}) // 450
		prov := &scriptedProvider{turns: []scriptedTurn{
			{events: []core.Event{core.Done{Usage: core.Usage{Prompt: 3600, Completion: 0}}}},
		}}
		pol, err := compact.New(prov, &captureFrontend{}, s, "S", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		dec := compact.Decorator(prov, pol)
		out, err := dec.Stream(context.Background(), core.Request{Messages: append([]core.Message{{Role: core.RoleSystem, Content: "S"}}, s.Messages...)})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for range out {
		}
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		if prov.calls() != 1 {
			t.Fatalf("no anchor: the factor must stay 1.0 (calls = %d, want none)", prov.calls())
		}
	})

	t.Run("a large tool spec stays out of the ratio", func(t *testing.T) {
		// reported - anchor isolates the delta: with a spec that is
		// dense in tokens but cheap in bytes, the old whole-request
		// denominator would inflate the factor to ~1.5; the delta ratio
		// is exactly 1.0.
		s := base()
		spec := core.ToolSpec{Name: "bash", Description: strings.Repeat("d", 2000), Schema: []byte(strings.Repeat("s", 4000))}
		prov := &scriptedProvider{turns: []scriptedTurn{
			{events: []core.Event{core.Done{Usage: core.Usage{Prompt: anchor + 100, Completion: 5}}}},
		}}
		pol, err := compact.New(prov, &captureFrontend{}, s, "S", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		dec := compact.Decorator(prov, pol)
		out, err := dec.Stream(context.Background(), core.Request{Messages: append([]core.Message{{Role: core.RoleSystem, Content: "S"}}, s.Messages...), Tools: []core.ToolSpec{spec}})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for range out {
		}
		// grow the delta to est 300: factor 1.0 -> size 800 (passthrough);
		// an inflated 1.5 -> 950 (compact). Passthrough proves 1.0.
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c2", Content: strings.Repeat("r", 800)}) // +200
		if _, err := pol.Assemble(context.Background(), s); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		if prov.calls() != 1 {
			t.Fatalf("the spec must not inflate the factor (calls = %d, want none)", prov.calls())
		}
	})
}

// TestMainCallMaxTokensClamped (named, decision 8): the pass-through
// stream's request carries the clamped MaxTokens; a request just under
// the trigger (size == Window - Reserve) gets MaxTokens == Reserve, not
// the floor 1 (the wrong-formula case).
func TestMainCallMaxTokensClamped(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 800, Reserve: 100, KeepRecent: 100}

	t.Run("anchored clamp", func(t *testing.T) {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 400)})
		s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 400), ContextTokens: 500})
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 800)}) // 200
		// size = 500 + 200 = 700 -> budget 300 -> min(800, 300)
		prov := &scriptedProvider{turns: []scriptedTurn{{events: []core.Event{core.Done{}}}}}
		pol, err := compact.New(prov, &captureFrontend{}, s, "S", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		dec := compact.Decorator(prov, pol)
		out, err := dec.Stream(context.Background(), core.Request{Messages: append([]core.Message{{Role: core.RoleSystem, Content: "S"}}, s.Messages...)})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for range out {
		}
		if got := prov.reqs()[0].MaxTokens; got != 300 {
			t.Fatalf("MaxTokens = %d, want 300 (Window 1000 - size 700)", got)
		}
	})

	t.Run("just under the trigger gets exactly the reserve", func(t *testing.T) {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 400)})
		s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 400), ContextTokens: 500})
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 1600)}) // 400
		// size = 500 + 400 = 900 == Window - Reserve: the wrong formula
		// (Window - Reserve - size) would give the floor 1 here.
		prov := &scriptedProvider{turns: []scriptedTurn{{events: []core.Event{core.Done{}}}}}
		pol, err := compact.New(prov, &captureFrontend{}, s, "S", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		dec := compact.Decorator(prov, pol)
		out, err := dec.Stream(context.Background(), core.Request{Messages: append([]core.Message{{Role: core.RoleSystem, Content: "S"}}, s.Messages...)})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for range out {
		}
		if got := prov.reqs()[0].MaxTokens; got != 100 {
			t.Fatalf("MaxTokens = %d, want the reserve 100 (not the floor 1)", got)
		}
	})

	t.Run("anchorless clamp", func(t *testing.T) {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 1400)}) // 350
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("q", 1400)}) // 350
		// size = est(system 200B = 50 + 700) = 750 -> budget 250
		prov := &scriptedProvider{turns: []scriptedTurn{{events: []core.Event{core.Done{}}}}}
		pol, err := compact.New(prov, &captureFrontend{}, s, strings.Repeat("s", 200), row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		dec := compact.Decorator(prov, pol)
		out, err := dec.Stream(context.Background(), core.Request{Messages: append([]core.Message{{Role: core.RoleSystem, Content: strings.Repeat("s", 200)}}, s.Messages...)})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for range out {
		}
		if got := prov.reqs()[0].MaxTokens; got != 250 {
			t.Fatalf("MaxTokens = %d, want 250 (Window 1000 - size 750)", got)
		}
	})

	t.Run("a request that still does not fit refuses loud", func(t *testing.T) {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 400), ContextTokens: 500})
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 2400)}) // 600
		// size = 1100 > Window: left -100 < the clamp's min 25 -> refuse
		// loud, not floor 1 — a kept batch larger than the model can hold
		// (decision 8's refuse-loud, surfaced so -p exits non-zero).
		prov := &scriptedProvider{turns: []scriptedTurn{{events: []core.Event{core.Done{}}}}}
		pol, err := compact.New(prov, &captureFrontend{}, s, "S", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		dec := compact.Decorator(prov, pol)
		out, err := dec.Stream(context.Background(), core.Request{Messages: append([]core.Message{{Role: core.RoleSystem, Content: "S"}}, s.Messages...)})
		if err == nil {
			for range out {
			}
			t.Fatal("a request that still does not fit must refuse loud, not stream")
		}
		if !strings.Contains(err.Error(), "exceeds the window") || !strings.Contains(err.Error(), "min 25") {
			t.Fatalf("the refusal must name the window gap and the minimum: %v", err)
		}
		if prov.calls() != 0 {
			t.Fatalf("the inner provider must not be reached: calls = %d", prov.calls())
		}
	})
}

// TestRecoveryKeptBatchOverrunsWindow (named, decision 8's refuse-loud):
// the main call fits the clamp, faults with context length, and the
// recovery compacts — but the kept batch (plus a large summary) still
// does not fit the window, so the retry's clamp refuses loud: a Fault is
// surfaced (so -p exits non-zero and the run record says fail), not a
// floor-1 one-token answer that logs success.
func TestRecoveryKeptBatchOverrunsWindow(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 400)}) // 100, the older prefix
	prov := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{core.Fault{Err: errors.New(contextFault)}}},
		summaryTurn(strings.Repeat("S", 800), core.Usage{Prompt: 5, Completion: 5}), // a large summary: ~200 tokens
	}}
	fe := &captureFrontend{}
	pol, err := compact.New(prov, fe, s, "S", row)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// the kept batch (assistant call + its result) is appended after
	// construction, so a recovery is owed; the main call fits the clamp
	// (1 + 100 + 52 + 800 = 953 <= 975), faults, and the recovery runs.
	s.Append(core.Message{
		Role: core.RoleAssistant, Content: strings.Repeat("a", 200),
		ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: []byte(`{}`)}},
	})
	s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 3200)}) // 800
	dec := compact.Decorator(prov, pol)
	out, err := dec.Stream(context.Background(), core.Request{Messages: append([]core.Message{{Role: core.RoleSystem, Content: "S"}}, s.Messages...)})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for ev := range out {
		fe.Notify(ev)
	}
	evs := fe.snapshot()
	// Compacted then a surfaced Fault — the retry (summary + kept batch
	// ~ 1 + 200 + 852 = 1053 > 975) refused loud, not floor 1.
	if len(evs) != 2 {
		t.Fatalf("frontend = %v, want Compacted then the surfaced refusal Fault", evs)
	}
	if _, ok := evs[0].(core.Compacted); !ok {
		t.Fatalf("event 0 = %T, want Compacted (the compact ran)", evs[0])
	}
	if f, ok := evs[1].(core.Fault); !ok || !strings.Contains(f.Err.Error(), "exceeds the window") {
		t.Fatalf("event 1 = %v, want the surfaced refusal Fault", evs[1])
	}
	if prov.calls() != 2 {
		t.Fatalf("provider calls = %d, want 2 (main + the summary call; the retry never reached the inner)", prov.calls())
	}
}

// steerFrontend serves scripted inputs and holds the loop's interrupt
// handle (the steering seam).
type steerFrontend struct {
	mu     sync.Mutex
	inputs []string
	cancel context.CancelFunc
	events []core.Event
}

func (f *steerFrontend) Input(ctx context.Context) (string, error) {
	if cancel, ok := core.InterruptFrom(ctx); ok {
		f.mu.Lock()
		f.cancel = cancel
		f.mu.Unlock()
	}
	f.mu.Lock()
	if len(f.inputs) == 0 {
		f.mu.Unlock()
		return "", io.EOF
	}
	s := f.inputs[0]
	f.inputs = f.inputs[1:]
	f.mu.Unlock()
	return s, nil
}

func (f *steerFrontend) Notify(ev core.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *steerFrontend) steal() context.CancelFunc {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.cancel
	f.cancel = nil
	return c
}

func (f *steerFrontend) snapshot() []core.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core.Event(nil), f.events...)
}

// TestSteerDuringRetry (named, decision 7's shape): the turn ctx dies in
// the retry window — the decorator's recovery reads it as the loop's
// existing interrupt path: no Fault, the turn breaks as an interrupt,
// the run continues.
func TestSteerDuringRetry(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 1200)}) // 300
	s.Append(core.Message{                                                          // 200
		Role: core.RoleAssistant, Content: strings.Repeat("a", 400),
		ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: []byte(`{}`)}},
	})
	s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 1200)}) // 300
	// est = 800 + system + the loop's user line: under the trigger (900),
	// so the Assemble passes through and the fault is the recovery's.

	summaryCalled := make(chan struct{})
	block := make(chan struct{})
	prov := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{core.Fault{Err: errors.New(contextFault)}}},
		{signal: func() { close(summaryCalled) }, blockOn: block, err: errors.New("steered: context canceled")},
		healthyTurn("ok", core.Usage{Prompt: 10, Completion: 1}),
	}}
	fe := &steerFrontend{inputs: []string{"go", "again"}}
	pol, err := compact.New(prov, fe, s, "S", row)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	k := rig.New(rig.WithProvider(compact.Decorator(prov, pol)), rig.WithFrontend(fe), rig.WithPolicy(pol))
	k.Session = s

	done := make(chan error, 1)
	go func() { done <- loop.Run(context.Background(), k) }()

	// the steer lands in the retry window: after the summary call is in
	// flight, before it returns.
	<-summaryCalled
	if cancel := fe.steal(); cancel == nil {
		t.Fatal("the loop must hand the frontend its interrupt handle")
	} else {
		cancel()
	}
	close(block)
	if err := <-done; err != nil {
		t.Fatalf("loop.Run: %v (a steer must not kill the run)", err)
	}

	evs := fe.snapshot()
	if len(evs) != 4 {
		t.Fatalf("frontend events = %v, want the interrupt boundary, the next turn, its boundary", evs)
	}
	if te, ok := evs[0].(core.TurnEnd); !ok || te.Reason != core.TurnInterrupt {
		t.Fatalf("event 0 = %v, want TurnEnd interrupt (no Fault — the steer's shape)", evs[0])
	}
	if _, ok := evs[0].(core.Fault); ok {
		t.Fatal("a Fault crossed the turn: the steer must read as an interrupt, not a fault")
	}
	if td, ok := evs[1].(core.TextDelta); !ok || td.Text != "ok" {
		t.Fatalf("the run must continue: event 1 = %v, want the next turn's text", evs[1])
	}
}

// TestRecoveryRewriteRaceFree (named, decision 7's gate): a full loop.Run
// through the decorator — the first model call faults with context
// length, the recovery compacts and re-issues, the retry succeeds. Run
// under -race (the gate): the rewrite (the relay goroutine) and the
// loop's post-close append are ordered by the channel, and the transcript
// shape holds — the summary row, the tail kept whole, the loop's answer
// appended after the close.
func TestRecoveryRewriteRaceFree(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 4000, MaxTokens: 500, Reserve: 100, KeepRecent: 600}
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 2000)}) // 500, the older prefix
	s.Append(core.Message{
		Role: core.RoleAssistant, Content: strings.Repeat("a", 200),
		ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: []byte(`{}`)}},
	})
	s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 2000)}) // 500
	prov := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{core.Fault{Err: errors.New(contextFault)}}},
		summaryTurn("SUM", core.Usage{Prompt: 5, Completion: 5}),
		healthyTurn("retry", core.Usage{Prompt: 6, Completion: 6}),
	}}
	fe := &steerFrontend{inputs: []string{"go"}}
	pol, err := compact.New(prov, fe, s, "S", row)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	k := rig.New(rig.WithProvider(compact.Decorator(prov, pol)), rig.WithFrontend(fe), rig.WithPolicy(pol))
	k.Session = s
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("loop.Run: %v", err)
	}
	// [summary row, assistant, tool (the tail pair), the user line, the
	// retry's answer] — the rewrite ordered before the loop's append.
	if len(s.Messages) != 5 {
		t.Fatalf("transcript = %d messages, want [summary, tail pair, user, retry]", len(s.Messages))
	}
	if s.Messages[0].Role != core.RoleUser || !strings.HasPrefix(s.Messages[0].Content, compact.SummaryMarker) {
		t.Fatalf("message 0 = %+v, want the summary row", s.Messages[0])
	}
	if s.Messages[1].Role != core.RoleAssistant || s.Messages[2].Role != core.RoleTool {
		t.Fatalf("the tail pair must stay whole: %+v", s.Messages[1:3])
	}
	if s.Messages[3].Role != core.RoleUser || s.Messages[3].Content != "go" {
		t.Fatalf("the user line must survive: %+v", s.Messages[3])
	}
	if s.Messages[4].Role != core.RoleAssistant || s.Messages[4].Content != "retry" {
		t.Fatalf("the loop's append must follow the rewrite: %+v", s.Messages[4])
	}
}
