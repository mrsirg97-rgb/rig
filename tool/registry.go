package tool

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed registry.json
var registryJSON []byte

type entry struct {
	Name       string          `json:"name"`
	Enabled    bool            `json:"enabled"`
	What       string          `json:"what"`
	Guidelines string          `json:"guidelines"`
	Reply      string          `json:"reply"`
	Schema     json.RawMessage `json:"schema"`
}

type Definition struct {
	name       string
	enabled    bool
	what       string
	guidelines string
	reply      string
	schema     string
}

var (
	order []string
	defs  map[string]Definition
)

func init() {
	var entries []entry
	if err := json.Unmarshal(registryJSON, &entries); err != nil {
		panic(fmt.Sprintf("tool/registry.json: %v", err))
	}
	defs = make(map[string]Definition, len(entries))
	for _, e := range entries {
		if e.Name == "" {
			panic("tool/registry.json: an entry has no name")
		}
		if _, dup := defs[e.Name]; dup {
			panic(fmt.Sprintf("tool/registry.json: %q appears twice", e.Name))
		}
		if e.What == "" || e.Guidelines == "" || e.Reply == "" {
			panic(fmt.Sprintf("tool/registry.json: %q lacks what, guidelines or reply", e.Name))
		}
		if !json.Valid(e.Schema) {
			panic(fmt.Sprintf("tool/registry.json: %q has no valid schema", e.Name))
		}
		defs[e.Name] = Definition{
			name: e.Name, enabled: e.Enabled,
			what: e.What, guidelines: e.Guidelines, reply: e.Reply,
			schema: string(e.Schema),
		}
		order = append(order, e.Name)
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
		if defs[n].enabled {
			out = append(out, n)
		}
	}
	return out
}

func AllNames() []string {
	return append([]string(nil), order...)
}

func (d Definition) Name() string { return d.name }

func (d Definition) Enabled() bool { return d.enabled }

func (d Definition) Description() string {
	return d.what + " guidelines: " + d.guidelines + " reply: " + d.reply
}

func (d Definition) Schema() json.RawMessage {
	return json.RawMessage(d.schema)
}

func (d Definition) Fill(slot, value string) Definition {
	d.what = strings.ReplaceAll(d.what, slot, value)
	d.guidelines = strings.ReplaceAll(d.guidelines, slot, value)
	d.reply = strings.ReplaceAll(d.reply, slot, value)
	d.schema = strings.ReplaceAll(d.schema, slot, value)
	return d
}
