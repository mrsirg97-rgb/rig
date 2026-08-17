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
var testRow = models.Model{ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 200}

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
		pol, err := compact.New(prov, fe, s, system, testRow)
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
		if evs := fe.snapshot(); len(evs) != 1 {
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
	worker := models.Model{ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
	brain := models.Model{ID: "brain", Window: 4000, MaxTokens: 500, Reserve: 200, KeepRecent: 500}
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
	piShape := models.Model{ID: "pi", Window: 100, MaxTokens: 10, Reserve: 100}
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
	row := models.Model{ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 200}

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
		// the summary input is [the summary prompt, the older prefix
		// (500)]: the clamp is Window - est(input), bounded by MaxTokens —
		// 3's honest budget, the reserve not subtracted twice. est is the
		// same stdlib rule as the policy's: per message, bytes/4 rounded
		// up, over the prompt file the test sits beside.
		prompt := reqs[0].Messages[0].Content
		est := (len(prompt)+3)/4 + 500
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
		// the input (the prompt + the 1250-token prefix) does not fit the
		// 1000-token window: the refusal names the row's numbers, with
		// the input's estimate computed the policy's way (the prompt is
		// the one file the test sits beside).
		prompt, perr := os.ReadFile("summary_prompt.txt")
		if perr != nil {
			t.Fatalf("read the prompt file: %v", perr)
		}
		wantEst := (len(prompt)+3)/4 + 1250
		msg := err.Error()
		for _, want := range []string{"local", "1000", fmt.Sprint(wantEst)} {
			if !strings.Contains(msg, want) {
				t.Fatalf("the refusal must name %q: %v", want, err)
			}
		}
	})
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

	evs := fe.snapshot()
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
	row := models.Model{ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
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
	row := models.Model{ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 200}
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
		if evs := fe.snapshot(); len(evs) != 1 {
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
		if evs := fe.snapshot(); len(evs) != 1 {
			t.Fatalf("the event must happen without the seam: %v", evs)
		}
	})
}
