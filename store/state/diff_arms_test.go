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

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/store/state"
	"github.com/mrsirg97-rgb/rig/v2/store/state/domain"
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
	seq, e := state.RecordMessage(ctx, db, sid, "assistant", "", nil, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e := state.RecordToolCall(ctx, db, "canonical-write", seq, "c1", "bash", `{"b":1, "a":2}`); e != nil {
		t.Fatalf("a decodable args string must land: %v", e)
	}
	tc := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, sid, seq, "c1").Row()
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
	if _, e := state.RecordMessage(ctx, db, sid, "user", "go", nil, nil, nil); e != nil {
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

	for i, id := range []string{"c1", "c2"} {
		tc := mustRead(t, db, func(c context.Context) (any, error) {
			return domain.NewToolCallDomain().GetToolCall(c, sid, int64(2+i), id).Row()
		}).(*domain.ToolCall)
		if tc == nil {
			t.Fatalf("call %s has no row: the row must always land", id)
		}
		if tc.Args != raw {
			t.Fatalf("args = %s, want the raw string %s (undecodable lands raw)", tc.Args, raw)
		}
	}
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
