package toolset

import (
	"context"
	"sort"
	"sync"

	"github.com/mrsirg97-rgb/rig/core"
)

type Table struct {
	mu      sync.RWMutex
	tools   []core.Tool
	plugins map[string]bool
}

func New(tools ...core.Tool) *Table {
	cp := make([]core.Tool, len(tools))
	copy(cp, tools)
	return &Table{tools: cp, plugins: make(map[string]bool)}
}

func (t *Table) Set(tools []core.Tool) {
	cp := make([]core.Tool, len(tools))
	copy(cp, tools)
	t.mu.Lock()
	t.tools = cp
	t.mu.Unlock()
}

// SetPlugins marks the currently-live plugin names (a swap carries its
// own plugin subset; names are disjoint from natives by the collision
// rule). IsPlugin answers the live plugin table's membership.
func (t *Table) SetPlugins(names ...string) {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	t.mu.Lock()
	t.plugins = set
	t.mu.Unlock()
}

// IsPlugin reports whether name is a currently-live plugin. It never
// answers true for a native (the collision rule keeps the sets
// disjoint), so a door over it admits plugins only.
func (t *Table) IsPlugin(name string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.plugins[name]
}

func (t *Table) List() []core.Tool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	cp := make([]core.Tool, len(t.tools))
	copy(cp, t.tools)
	return cp
}

func (t *Table) Specs() []core.ToolSpec {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]core.ToolSpec, 0, len(t.tools))
	for _, tool := range t.tools {
		out = append(out, core.ToolSpec{Name: tool.Name(), Description: tool.Description(), Schema: tool.Schema()})
	}
	return out
}

// NativeSpecs is the request's tool list (SPEC_GROWTH 9): every tool the
// table does not carry as a plugin — the natives, including the plugin
// door. Plugin schemas stay out of the request; the door's own schema
// carries their names' enum. Carry stamps this, not Specs().
func (t *Table) NativeSpecs() []core.ToolSpec {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]core.ToolSpec, 0, len(t.tools))
	for _, tool := range t.tools {
		if t.plugins[tool.Name()] {
			continue
		}
		out = append(out, core.ToolSpec{Name: tool.Name(), Description: tool.Description(), Schema: tool.Schema()})
	}
	return out
}

// PluginNames is the live plugin set, sorted (the door's schema enum, the
// deterministic order decision 2's). The swap's names ride here.
func (t *Table) PluginNames() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	names := make([]string, 0, len(t.plugins))
	for name := range t.plugins {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Tool resolves a live table tool by name (the plugin door's lookup seam,
// SPEC_GROWTH 9). It serves natives and plugins alike.
func (t *Table) Tool(name string) (core.Tool, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, tool := range t.tools {
		if tool.Name() == name {
			return tool, true
		}
	}
	return nil, false
}

func (t *Table) tool(name string) (core.Tool, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, tool := range t.tools {
		if tool.Name() == name {
			return tool, true
		}
	}
	return nil, false
}

func Resolve(t *Table) core.ToolMiddleware {
	return core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec {
		return func(ctx context.Context, call core.ToolCall) (string, error) {
			if tool, ok := t.tool(call.Name); ok {
				return tool.Exec(ctx, call.Args)
			}
			return next(ctx, call)
		}
	})
}

func Carry(t *Table, inner core.Provider) core.Provider {
	return carrier{table: t, inner: inner}
}

type carrier struct {
	table *Table
	inner core.Provider
}

func (c carrier) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	req.Tools = c.table.NativeSpecs()
	return c.inner.Stream(ctx, req)
}
