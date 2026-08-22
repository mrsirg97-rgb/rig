package state_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store/state"
	"github.com/mrsirg97-rgb/rig/store/state/domain"
)

func TestCanonicalIgnoresKeyOrderAndWhitespaceValuesMatter(t *testing.T) {
	parts := []struct{ key, val string }{
		{"a", `1`},
		{"b", `"two"`},
		{"c", `[1,2,3]`},
		{"d", `{"e":true,"f":null}`},
	}
	byKey := map[string]string{}
	for _, p := range parts {
		byKey[p.key] = p.val
	}
	want := `{"a":1,"b":"two","c":[1,2,3],"d":{"e":true,"f":null}}`

	perms := permutations([]string{"a", "b", "c", "d"})
	if len(perms) != 24 {
		t.Fatalf("permutations = %d, want 24", len(perms))
	}
	seps := []string{",", ", ", " ,   "}
	for _, pm := range perms {
		for _, sep := range seps {
			s := "{" + joinBy(func(i int) string { return `"` + pm[i] + `":` + byKey[pm[i]] }, len(pm), sep) + "}"
			got, err := state.CanonicalArgs(s)
			if err != nil {
				t.Fatalf("CanonicalArgs(%s): %v", s, err)
			}
			if got != want {
				t.Fatalf("CanonicalArgs(%s) = %s, want %s (key order and whitespace are noise)", s, got, want)
			}
		}
	}

	changed := []string{
		`{"a":2,"b":"two","c":[1,2,3],"d":{"e":true,"f":null}}`,
		`{"a":"1","b":"two","c":[1,2,3],"d":{"e":true,"f":null}}`,
		`{"a":1,"b":"tow","c":[1,2,3],"d":{"e":true,"f":null}}`,
		`{"a":1,"b":"two","c":[1,3,2],"d":{"e":true,"f":null}}`,
		`{"a":1,"b":"two","c":[1,2,3],"d":{"e":false,"f":null}}`,
		`{"a":1,"b":"two","c":[1,2,3],"d":{"e":true,"f":0}}`,
	}
	for _, s := range changed {
		got, err := state.CanonicalArgs(s)
		if err != nil {
			t.Fatalf("CanonicalArgs(%s): %v", s, err)
		}
		if got == want {
			t.Fatalf("CanonicalArgs(%s) = %s, want a different canonical form (values do matter)", s, got)
		}
	}
}

func TestCanonicalDistinguishesNamedPairs(t *testing.T) {
	cases := [][2]string{
		{`1`, `"1"`},
		{`{"a":1}`, `{"a":null}`},
		{`{"a":1}`, `{}`},
	}
	for _, c := range cases {
		a, err := state.CanonicalArgs(c[0])
		if err != nil {
			t.Fatalf("CanonicalArgs(%s): %v", c[0], err)
		}
		b, err := state.CanonicalArgs(c[1])
		if err != nil {
			t.Fatalf("CanonicalArgs(%s): %v", c[1], err)
		}
		if a == b {
			t.Fatalf("CanonicalArgs(%s) = CanonicalArgs(%s) = %s, want different", c[0], c[1], a)
		}
	}
	same, err := state.CanonicalArgs(`1`)
	if err != nil {
		t.Fatal(err)
	}
	oneDotZero, err := state.CanonicalArgs(`1.0`)
	if err != nil {
		t.Fatal(err)
	}
	if same != oneDotZero {
		t.Fatalf("1 and 1.0 decode to the same JSON value: CanonicalArgs(%q) = %q, want %q", `1.0`, oneDotZero, same)
	}
}

func TestRecordToolCallStoresCanonicalForm(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	sid := "canonical-write"
	if e := state.RecordSession(ctx, db, sid, "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	seq, e := state.RecordMessage(ctx, db, sid, "assistant", "", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e := state.RecordToolCall(ctx, db, seq, "c1", "bash", `{"b":1, "a":2}`); e != nil {
		t.Fatalf("a decodable args string must land: %v", e)
	}
	tc := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, "c1").Row()
	}).(*domain.ToolCall)
	if tc.Args != `{"a":2,"b":1}` {
		t.Fatalf("args = %s, want the canonical form {\"a\":2,\"b\":1}", tc.Args)
	}
}

func TestRecorderUndecodableArgsLandsRawAndSpeaks(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	sid := "rec-undec"
	if e := state.RecordSession(ctx, db, sid, "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	if _, e := state.RecordMessage(ctx, db, sid, "user", "go", nil, nil); e != nil {
		t.Fatal(e)
	}
	rec := state.NewRecorder(&nullFrontend{}, db, "/w", "m", "v", sid, core.NewSession())
	raw := `{"command": ls}`
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	rec.Notify(core.ToolCallEvent{Call: core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(raw)}})
	rec.Notify(core.Done{StopReason: "end_turn"})

	rec.Notify(core.ToolCallEvent{Call: core.ToolCall{ID: "c2", Name: "bash", Args: json.RawMessage(raw)}})
	rec.Notify(core.Done{StopReason: "end_turn"})
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)

	var probe any
	decodeErr := json.Unmarshal([]byte(raw), &probe)
	if decodeErr == nil {
		t.Fatal("the test's args string must fail to decode")
	}
	for _, id := range []string{"c1", "c2"} {
		want := fmt.Sprintf("rig state: session %s: tool call %s: %v", sid, id, decodeErr)
		if !strings.Contains(string(out), want) {
			t.Fatalf("the recorder must speak %q, said %q", want, out)
		}
	}

	for _, id := range []string{"c1", "c2"} {
		tc := mustRead(t, db, func(c context.Context) (any, error) {
			return domain.NewToolCallDomain().GetToolCall(c, id).Row()
		}).(*domain.ToolCall)
		if tc == nil {
			t.Fatalf("call %s has no row: the row must always land", id)
		}
		if tc.Args != raw {
			t.Fatalf("args = %s, want the raw string %s (undecodable lands raw)", tc.Args, raw)
		}
	}
}

func TestRecentToolCallsReturnsNewestFirst(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	sid := "recent"
	if e := state.RecordSession(ctx, db, sid, "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	results := []string{"r1", "r2", "r3"}
	var seqs []int64
	for i, res := range results {
		seq, e := state.RecordMessage(ctx, db, sid, "assistant", "", nil, nil)
		if e != nil {
			t.Fatal(e)
		}
		seqs = append(seqs, seq)
		id := fmt.Sprintf("c%d", i+1)
		if e := state.RecordToolCall(ctx, db, seq, id, "bash", `{"command":"ls"}`); e != nil {
			t.Fatal(e)
		}
		if e := state.RecordToolResult(ctx, db, id, res, nil); e != nil {
			t.Fatal(e)
		}
	}
	args := `{"command":"ls"}`
	one, err := state.RecentToolCalls(ctx, db, sid, "bash", args, 1)
	if err != nil {
		t.Fatalf("RecentToolCalls(n=1): %v", err)
	}
	if len(one) != 2 {
		t.Fatalf("n=1 returns %d rows, want the 2 most recent", len(one))
	}
	if one[0].Result != "r3" || one[1].Result != "r2" {
		t.Fatalf("newest first = %q %q, want r3 r2", one[0].Result, one[1].Result)
	}
	if one[0].Seq != seqs[2] || one[1].Seq != seqs[1] {
		t.Fatalf("seqs = %d %d, want %d %d (newest first)", one[0].Seq, one[1].Seq, seqs[2], seqs[1])
	}
	if one[0].StartedAt.Before(one[1].StartedAt) {
		t.Fatalf("started_at = %v then %v, want newest first", one[0].StartedAt, one[1].StartedAt)
	}
	two, err := state.RecentToolCalls(ctx, db, sid, "bash", args, 2)
	if err != nil {
		t.Fatalf("RecentToolCalls(n=2): %v", err)
	}
	if len(two) != 3 {
		t.Fatalf("n=2 returns %d rows, want 3", len(two))
	}
	if two[0].Result != "r3" || two[1].Result != "r2" || two[2].Result != "r1" {
		t.Fatalf("n=2 newest first = %q %q %q, want r3 r2 r1", two[0].Result, two[1].Result, two[2].Result)
	}
}

func TestRecentToolCallsInflightInvisible(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	sid := "inflight"
	if e := state.RecordSession(ctx, db, sid, "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	seq, e := state.RecordMessage(ctx, db, sid, "assistant", "", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e := state.RecordToolCall(ctx, db, seq, "c1", "bash", `{"command":"ls"}`); e != nil {
		t.Fatal(e)
	}
	if e := state.RecordToolResult(ctx, db, "c1", "r1", nil); e != nil {
		t.Fatal(e)
	}
	seq, e = state.RecordMessage(ctx, db, sid, "assistant", "", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e := state.RecordToolCall(ctx, db, seq, "c2", "bash", `{"command":"ls"}`); e != nil {
		t.Fatal(e)
	}
	if e := state.RecordToolResult(ctx, db, "c2", "r2", nil); e != nil {
		t.Fatal(e)
	}

	seq, e = state.RecordMessage(ctx, db, sid, "assistant", "", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e := state.RecordToolCall(ctx, db, seq, "c3", "bash", `{"command":"ls"}`); e != nil {
		t.Fatal(e)
	}
	got, err := state.RecentToolCalls(ctx, db, sid, "bash", `{"command":"ls"}`, 1)
	if err != nil {
		t.Fatalf("RecentToolCalls: %v", err)
	}
	if len(got) != 2 || got[0].Result != "r2" || got[1].Result != "r1" {
		t.Fatalf("rows = %d (%q), want the two completed rows r2 r1 (the in-flight row is invisible)", len(got), resultsOf(got))
	}
}

func TestRecentToolCallsSessionScope(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	if e := state.RecordSession(ctx, db, "world-a", "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	if e := state.RecordSession(ctx, db, "world-b", "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	makeCall := func(sid, id, res string) {
		t.Helper()
		seq, e := state.RecordMessage(ctx, db, sid, "assistant", "", nil, nil)
		if e != nil {
			t.Fatal(e)
		}
		if e := state.RecordToolCall(ctx, db, seq, id, "bash", `{"command":"ls"}`); e != nil {
			t.Fatal(e)
		}
		if e := state.RecordToolResult(ctx, db, id, res, nil); e != nil {
			t.Fatal(e)
		}
	}
	makeCall("world-a", "a1", "ra")
	makeCall("world-b", "b1", "rb1")
	makeCall("world-b", "b2", "rb2")
	a, err := state.RecentToolCalls(ctx, db, "world-a", "bash", `{"command":"ls"}`, 1)
	if err != nil {
		t.Fatalf("RecentToolCalls(world-a): %v", err)
	}
	if len(a) != 1 || a[0].Result != "ra" {
		t.Fatalf("world-a rows = %d (%s), want only ra (another session is another world)", len(a), resultsOf(a))
	}
	b, err := state.RecentToolCalls(ctx, db, "world-b", "bash", `{"command":"ls"}`, 1)
	if err != nil {
		t.Fatalf("RecentToolCalls(world-b): %v", err)
	}
	if len(b) != 2 || b[0].Result != "rb2" || b[1].Result != "rb1" {
		t.Fatalf("world-b rows = %d (%s), want rb2 rb1", len(b), resultsOf(b))
	}
}

func TestRecentToolCallsWorldBoundary(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	sid := "world"
	if e := state.RecordSession(ctx, db, sid, "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	args := `{"command":"ls"}`
	seq1, e := state.RecordMessage(ctx, db, sid, "assistant", "", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e := state.RecordToolCall(ctx, db, seq1, "c1", "bash", args); e != nil {
		t.Fatal(e)
	}
	if e := state.RecordToolResult(ctx, db, "c1", "r1", nil); e != nil {
		t.Fatal(e)
	}

	sess := core.NewSession()
	sess.Append(core.Message{Role: core.RoleUser, Content: "[compaction] the summary"})
	sess.Append(core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: json.RawMessage(args)}}})
	sess.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: "r1"})
	rec := state.NewRecorder(&nullFrontend{}, db, "/w", "m", "v", sid, sess)
	rec.Notify(core.Compacted{Summary: "[compaction] the summary"})

	rows, err := state.RecentToolCalls(ctx, db, sid, "bash", args, 1)
	if err != nil {
		t.Fatalf("RecentToolCalls: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d (%s), want 1 (the re-landed copy; the original is before the marker and another world)", len(rows), resultsOf(rows))
	}
	if rows[0].Result != "r1" || rows[0].Seq <= seq1 {
		t.Fatalf("the in-scope row = %q seq %d, want r1 at a fresh seq past the marker (the original's seq is %d)", rows[0].Result, rows[0].Seq, seq1)
	}

	seq2, e := state.RecordMessage(ctx, db, sid, "assistant", "", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e := state.RecordToolCall(ctx, db, seq2, "c2", "bash", args); e != nil {
		t.Fatal(e)
	}
	if e := state.RecordToolResult(ctx, db, "c2", "r2", nil); e != nil {
		t.Fatal(e)
	}
	rows, err = state.RecentToolCalls(ctx, db, sid, "bash", args, 1)
	if err != nil {
		t.Fatalf("RecentToolCalls: %v", err)
	}
	if len(rows) != 2 || rows[0].Result != "r2" || rows[1].Result != "r1" || rows[1].Seq <= seq1 {
		t.Fatalf("rows = %d (%s), want r2 over the re-landed r1 (the tail's memory carries forward)", len(rows), resultsOf(rows))
	}

	sid2 := "markerless"
	if e := state.RecordSession(ctx, db, sid2, "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	for i, res := range []string{"a", "b"} {
		seq, e := state.RecordMessage(ctx, db, sid2, "assistant", "", nil, nil)
		if e != nil {
			t.Fatal(e)
		}
		id := fmt.Sprintf("m%d", i+1)
		if e := state.RecordToolCall(ctx, db, seq, id, "bash", args); e != nil {
			t.Fatal(e)
		}
		if e := state.RecordToolResult(ctx, db, id, res, nil); e != nil {
			t.Fatal(e)
		}
	}
	whole, err := state.RecentToolCalls(ctx, db, sid2, "bash", args, 1)
	if err != nil {
		t.Fatalf("RecentToolCalls(markerless): %v", err)
	}
	if len(whole) != 2 {
		t.Fatalf("markerless rows = %d (%s), want 2 (no marker, the session reads whole)", len(whole), resultsOf(whole))
	}
}

func TestRecentToolCallsInterleavingKeepsThePair(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	sid := "interleave"
	if e := state.RecordSession(ctx, db, sid, "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	call := func(id, name, args, res string) {
		t.Helper()
		seq, e := state.RecordMessage(ctx, db, sid, "assistant", "", nil, nil)
		if e != nil {
			t.Fatal(e)
		}
		if e := state.RecordToolCall(ctx, db, seq, id, name, args); e != nil {
			t.Fatal(e)
		}
		if res != "" {
			if e := state.RecordToolResult(ctx, db, id, res, nil); e != nil {
				t.Fatal(e)
			}
		}
	}
	call("c1", "bash", `{"command":"ls"}`, "r1")
	call("c2", "bash", `{"command":"pwd"}`, "other-tool-args")
	call("c3", "read", `{"path":"/x"}`, "other-tool")
	call("c4", "bash", `{"command":"ls"}`, "r2")
	got, err := state.RecentToolCalls(ctx, db, sid, "bash", `{"command":"ls"}`, 1)
	if err != nil {
		t.Fatalf("RecentToolCalls: %v", err)
	}
	if len(got) != 2 || got[0].Result != "r2" || got[1].Result != "r1" {
		t.Fatalf("rows = %d (%s), want the pair r2 r1 (interleaving does not displace it)", len(got), resultsOf(got))
	}
}

func TestRecentToolCallsTotalOrder(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	sid := "tie"
	if e := state.RecordSession(ctx, db, sid, "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	seq1, e := state.RecordMessage(ctx, db, sid, "assistant", "", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	seq2, e := state.RecordMessage(ctx, db, sid, "assistant", "", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	at := time.Date(2026, 8, 16, 9, 58, 12, 0, time.UTC)
	seed := func(id string, messageSeq int64, res string) {
		t.Helper()
		if e := state.RecordToolCall(ctx, db, messageSeq, id, "bash", `{"command":"ls"}`); e != nil {

			t.Fatal(e)
		}

		mustRead(t, db, func(c context.Context) (any, error) {
			tc, err := domain.NewToolCallDomain().GetToolCall(c, id).Row()
			if err != nil {
				return nil, err
			}
			tc.StartedAt = at
			_, err = domain.NewToolCallDomain().UpdateToolCall(c, *tc)
			return nil, err
		})
		if e := state.RecordToolResult(ctx, db, id, res, nil); e != nil {
			t.Fatal(e)
		}
	}
	seed("a", seq1, "ra")
	seed("b", seq1, "rb")
	seed("c", seq2, "rc")
	got, err := state.RecentToolCalls(ctx, db, sid, "bash", `{"command":"ls"}`, 2)
	if err != nil {
		t.Fatalf("RecentToolCalls: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("rows = %d, want 3", len(got))
	}
	if got[0].Result != "rc" || got[1].Result != "rb" || got[2].Result != "ra" {
		t.Fatalf("order = %s, want rc (later message), then rb over ra (id desc within the message)", resultsOf(got))
	}
}

func resultsOf(rows []state.Observation) string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Result
	}
	return strings.Join(out, " ")
}

func permutations(xs []string) [][]string {
	var out [][]string
	var rec func(rest []string, acc []string)
	rec = func(rest []string, acc []string) {
		if len(rest) == 0 {
			acc = append(append([]string(nil), acc...), rest...)
			out = append(out, acc)
			return
		}
		for i := range rest {
			nr := append(append([]string(nil), rest[:i]...), rest[i+1:]...)
			rec(nr, append(acc, rest[i]))
		}
	}
	rec(xs, nil)
	sort.Slice(out, func(i, j int) bool {
		for k := range out[i] {
			if out[i][k] != out[j][k] {
				return out[i][k] < out[j][k]
			}
		}
		return false
	})
	return out
}

func joinBy(get func(int) string, n int, sep string) string {
	if n == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(get(0))
	for i := 1; i < n; i++ {
		b.WriteString(sep)
		b.WriteString(get(i))
	}
	return b.String()
}
