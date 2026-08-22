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

func (t *Table) SetPlugins(names ...string) {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	t.mu.Lock()
	t.plugins = set
	t.mu.Unlock()
}

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
