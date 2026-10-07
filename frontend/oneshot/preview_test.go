package oneshot

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
)

type capture struct {
	mu     sync.Mutex
	events []core.Event
}

func (c *capture) Id() int64 { return 3 }

func (c *capture) Send(ctx context.Context, callback func(error), messages ...broadcast.Message) {
	c.mu.Lock()
	for _, m := range messages {
		if ev := m.Event(); ev != nil {
			c.events = append(c.events, ev)
		}
	}
	c.mu.Unlock()
	callback(nil)
}

func (c *capture) Recv(context.Context, func(error, ...broadcast.Message)) {}
func (c *capture) Close()                                                  {}

func (c *capture) sent() []core.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]core.Event, len(c.events))
	copy(out, c.events)
	return out
}

func TestAPublishedToolCallIsNameAndOneBoundedArgument(t *testing.T) {
	rig := &capture{}
	o := &OneShot{Fleet: rig}
	o.Notify(core.ToolStart{Call: core.ToolCall{
		ID:   "c1",
		Name: "edit",
		Args: json.RawMessage(`{"path":"tool/file/edit.go","old":"func a() {\n\tone\n}","new":"func a() {\n\ttwo\n}"}`),
	}})
	ev := only(t, rig)
	call := ev.(core.ToolStart).Call
	if call.Name != "edit" {
		t.Fatalf("the tool's name crosses: %q", call.Name)
	}
	if got := callPreview(t, call.Args); got != "tool/file/edit.go" {
		t.Fatalf("the argument that names the work crosses: %q", got)
	}
}

func TestATenKilobyteWriteCrossesAsOneLineOfEightyCharacters(t *testing.T) {
	body := strings.Repeat("a very long line of go source\n", 400)
	if len(body) < 10_000 {
		t.Fatalf("the fixture is not 10 KB: %d", len(body))
	}
	cases := []struct{ name, args string }{
		{"write", mustArgs(t, map[string]any{"content": body, "path": "docs/a-page.md"})},
		{"edit", mustArgs(t, map[string]any{"path": "docs/a-page.md", "old": body, "new": body})},
		{"bash", mustArgs(t, map[string]any{"command": "head -1 " + strings.Repeat("x", 300)})},
		{"no-arguments", `{}`},
		{"not-an-object", `"just a string"`},
	}
	for _, tc := range cases {
		rig := &capture{}
		o := &OneShot{Fleet: rig}
		o.Notify(core.ToolStart{Call: core.ToolCall{ID: "c", Name: tc.name, Args: json.RawMessage(tc.args)}})
		call := only(t, rig).(core.ToolStart).Call
		preview := callPreview(t, call.Args)
		if utf8.RuneCountInString(preview) > argBound {
			t.Fatalf("%s: %d characters crossed, the bound is %d: %q", tc.name, utf8.RuneCountInString(preview), argBound, preview)
		}
		if strings.ContainsAny(preview, "\n\r") {
			t.Fatalf("%s: a body crossed: %q", tc.name, preview)
		}
		if strings.Contains(preview, "very long line of go source") && tc.name != "bash" {
			t.Fatalf("%s: the body crossed: %q", tc.name, preview)
		}
	}
}

func TestAToolCallCrossesWithoutATransportAndWithoutAnErrWriter(t *testing.T) {
	o := &OneShot{}
	o.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "read", Args: json.RawMessage(`{"path":"x"}`)}})
	o.Notify(core.ToolResult{ID: "c1", Content: "y"})
}

func only(t *testing.T, c *capture) core.Event {
	t.Helper()
	events := c.sent()
	if len(events) != 1 {
		t.Fatalf("one bounded frame per tool start, got %d: %v", len(events), events)
	}
	return events[0]
}

func callPreview(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("the published argument is a JSON string, got %s: %v", raw, err)
	}
	return s
}

func mustArgs(t *testing.T, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
