package tool

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

//go:embed registry.json
var registryJSON []byte

type Definition interface {
	Name() string
	Enabled() bool
	Description() string
	Schema() json.RawMessage
}

type entry struct {
	N string          `json:"name"`
	E bool            `json:"enabled"`
	W string          `json:"what"`
	G string          `json:"guidelines"`
	R string          `json:"reply"`
	O []string        `json:"operator"`
	M []string        `json:"mutates"`
	D string          `json:"defaultAction"`
	S json.RawMessage `json:"schema"`
}

func (e *entry) Name() string { return e.N }

func (e *entry) Enabled() bool { return e.E }

func (e *entry) Description() string {
	return e.W + " guidelines: " + e.G + " reply: " + e.R
}

func (e *entry) Schema() json.RawMessage {
	return append(json.RawMessage(nil), e.S...)
}

type filled struct {
	Definition
	slot  string
	value string
}

func Fill(d Definition, slot, value string) Definition {
	return &filled{d, slot, value}
}

func (f *filled) Description() string {
	return strings.ReplaceAll(f.Definition.Description(), f.slot, f.value)
}

func (f *filled) Schema() json.RawMessage {
	return json.RawMessage(strings.ReplaceAll(string(f.Definition.Schema()), f.slot, f.value))
}

var (
	order []string
	defs  map[string]*entry
)

func init() {
	var entries []*entry
	if err := json.Unmarshal(registryJSON, &entries); err != nil {
		panic(fmt.Sprintf("tool/registry.json: %v", err))
	}
	defs = make(map[string]*entry, len(entries))
	for _, e := range entries {
		if e.N == "" {
			panic("tool/registry.json: an entry has no name")
		}
		if _, dup := defs[e.N]; dup {
			panic(fmt.Sprintf("tool/registry.json: %q appears twice", e.N))
		}
		if e.W == "" || e.G == "" || e.R == "" {
			panic(fmt.Sprintf("tool/registry.json: %q lacks what, guidelines or reply", e.N))
		}
		if !json.Valid(e.S) {
			panic(fmt.Sprintf("tool/registry.json: %q has no valid schema", e.N))
		}
		defs[e.N] = e
		order = append(order, e.N)
	}
}

func Def(name string) Definition {
	d, ok := defs[name]
	if !ok {
		panic(fmt.Sprintf("tool/registry.json: no entry for %q", name))
	}
	return d
}

func Names() []string {
	out := make([]string, 0, len(order))
	for _, n := range order {
		if defs[n].Enabled() {
			out = append(out, n)
		}
	}
	return out
}

const MaxCommandBoundMs = 86_400_000

func BoundMs(who string, raw *int64) (time.Duration, error) {
	if raw == nil {
		return 0, nil
	}
	if *raw < 1000 {
		return 0, fmt.Errorf("%s: timeoutMs must be at least 1000 ms (omit the field for no bound), got %d", who, *raw)
	}
	if *raw > MaxCommandBoundMs {
		return 0, fmt.Errorf("%s: timeoutMs ceiling is %d ms (24h), got %d", who, MaxCommandBoundMs, *raw)
	}
	return time.Duration(*raw) * time.Millisecond, nil
}

func DeferNeedsBound(who string) string {
	return who + ": defer needs timeoutMs, the bound a background run is killed at"
}

func FirstLine(s string) string {
	line := strings.SplitN(strings.TrimSpace(s), "\n", 2)[0]
	line = strings.TrimSpace(line)
	if len(line) > 60 {
		line = line[:60] + "…"
	}
	return line
}

func ExitTrailer(who string, exit int, dur time.Duration, id, log string, timedOut bool, bound time.Duration) string {
	s := fmt.Sprintf("%s: exit %d · %dms · run %s · log %s", who, exit, dur.Milliseconds(), id, log)
	if timedOut && bound > 0 {
		s += fmt.Sprintf(" · killed at timeoutMs=%d", bound.Milliseconds())
	}
	return s
}

func Operator(name string) []string {
	e, ok := defs[name]
	if !ok {
		return nil
	}
	return e.O
}

func Mutates(name, action string) bool {
	e, ok := defs[name]
	if !ok {
		return true
	}
	if action == "" {
		action = e.D
	}
	for _, a := range e.M {
		if a == "*" || a == action {
			return true
		}
	}
	return false
}

func AllNames() []string {
	return append([]string(nil), order...)
}
