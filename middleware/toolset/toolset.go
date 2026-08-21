package toolset

import (
	"context"
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
	req.Tools = c.table.Specs()
	return c.inner.Stream(ctx, req)
}
