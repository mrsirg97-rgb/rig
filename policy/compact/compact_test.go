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

var testRow = models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 200}

func TestBelowTriggerIsPassthroughByteIdentical(t *testing.T) {
	const system = "S"

	t.Run("anchored at the boundary is passthrough", func(t *testing.T) {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 400)})
		s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 200), ContextTokens: 800})
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 400)})

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

func TestTriggerMathPerModelOneConfig(t *testing.T) {
	worker := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
	brain := models.Model{Role: models.RoleInteractive, ID: "brain", Window: 4000, MaxTokens: 500, Reserve: 200, KeepRecent: 500}
	table, err := models.New(worker, brain)
	if err != nil {
		t.Fatalf("one table carrying both rows: %v", err)
	}

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

	piShape := models.Model{Role: models.RoleInteractive, ID: "pi", Window: 100, MaxTokens: 10, Reserve: 100}
	if err := piShape.Check(); err == nil {
		t.Fatal("Reserve >= Window must be refused at construction (the pi shape)")
	}
	_ = table
}

func TestSummaryMaxTokensClamped(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 200}

	t.Run("honest budget", func(t *testing.T) {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 2000)})
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("q", 2000)})
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
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 5000)})
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("q", 100)})
		prov := &scriptedProvider{}
		pol, err := compact.New(prov, &captureFrontend{}, s, "", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = pol.Assemble(context.Background(), s)
		if err == nil {
			t.Fatal("Assemble = ok, want the loud failure")
		}

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
		Role: core.RoleAssistant, ContextTokens: 1800,
		ToolCalls: []core.ToolCall{{ID: "c2", Name: "bash", Args: []byte(`{"command":"echo X"}`)}},
	})
	s.Append(core.Message{Role: core.RoleTool, ToolID: "c2", Content: "X"})

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
		"user: reply with only X",
	} {
		if !strings.Contains(c, want) {
			t.Fatalf("the quoted transcript must render %q", want)
		}
	}

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

	if len(s.Messages) != 3 {
		t.Fatalf("transcript = %d messages, want the summary + the kept tail", len(s.Messages))
	}
	if s.Messages[0].Role != core.RoleUser || s.Messages[0].Content != compact.SummaryMarker+summary {
		t.Fatalf("the rewritten transcript must start with the marked summary row: %+v", s.Messages[0])
	}
	if s.Messages[1].ToolCalls[0].ID != "c2" || s.Messages[2].Role != core.RoleTool {
		t.Fatalf("the kept tail must be the pair, whole: %+v", s.Messages[1:])
	}

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

func TestCompactedEventBeforeTheNextCall(t *testing.T) {
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 2000)})
	s.Append(core.Message{
		Role: core.RoleAssistant, Content: strings.Repeat("a", 200),
		ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: []byte(`{}`)}},
	})
	s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 2000)})

	prov := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{core.TextDelta{Text: "THE SUMMARY"}, core.Done{Usage: core.Usage{Prompt: 777, Completion: 33}}}},

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

func TestSecondCompactionFoldsTheFirst(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p1", 1000)})
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p2", 1000)})

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

	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p3", 400)})
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p4", 1600)})
	if _, err := pol.Assemble(context.Background(), s); err != nil {
		t.Fatalf("second Assemble: %v", err)
	}

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

func TestAutoReflectLandsDedupesAndNeverFails(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 200}
	cwd := t.TempDir()

	t.Run("the reflection lands with pane's session_compact shape", func(t *testing.T) {
		rdb, _, err := store.Open(filepath.Join(cwd, "rem.sqlite"), remstore.Statements(), remstore.SchemaVersion)
		if err != nil {
			t.Fatalf("open the rem store: %v", err)
		}
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p1", 1000)})
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p2", 1000)})
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

		if len(s.Messages) != 2 || s.Messages[0].Content != compact.SummaryMarker+"S" {
			t.Fatalf("the compact must happen without the seam: %+v", s.Messages)
		}
		if evs := stripCue(fe.snapshot()); len(evs) != 1 {
			t.Fatalf("the event must happen without the seam: %v", evs)
		}
	})
}

func TestSummaryEffortIsTheRow(t *testing.T) {
	compactFixture := func(row models.Model) (*scriptedProvider, *core.Session) {
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 2000)})
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("q", 2000)})
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
		row := base
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

func TestOversizedOlderCompactsTheOldestSliceThatFits(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 1200)})
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 1200)})
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("q", 1200)})
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("b", 1200)})
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("t", 100)})

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

func TestOversizedOlderSliceRespectsTheCallBoundary(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 1200)})
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 400), ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: []byte(`{"command":"ls"}`)}}})
	s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 2000)})
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("b", 1200)})
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("t", 100)})

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
