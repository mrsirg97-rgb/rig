// Package toolset is the root's live tool list (SPEC_PLUGINS 8): the
// kernel's tools as a per-turn fact, not a startup snapshot. The loop
// borrows two things per turn — the provider (the request's tools
// array) and the execution chain (the middleware) — and the root owns
// both ends of this table: Carry stamps the table's specs into every
// request before delegating, and Resolve serves the table's tools,
// falling through to the inner exec for a name the table does not
// carry. A swap (the plugins_reload's, the /plugins reload's, the
// approve's tail) is one atomic write: the next turn's request carries
// the new list and the new tools execute, by construction — the
// models-switch's semantics (SPEC_COMMANDS 6), zero loop lines.
//
// Stdlib plus core, nothing else: the leaf owns the fact, the root
// wires it.
package toolset

import (
	"context"
	"sync"

	"github.com/mrsirg97-rgb/rig/core"
)

// Table is the live list: ordered (the wire's order), swapped
// atomically (a reader sees one list or the next, never a mix).
type Table struct {
	mu    sync.RWMutex
	tools []core.Tool
}

// New copies the tools: the caller's slice is not retained.
func New(tools ...core.Tool) *Table {
	cp := make([]core.Tool, len(tools))
	copy(cp, tools)
	return &Table{tools: cp}
}

// Set swaps the list (the reload's rebuild). Copy-in: a later mutation
// of the caller's slice does not reach the table.
func (t *Table) Set(tools []core.Tool) {
	cp := make([]core.Tool, len(tools))
	copy(cp, tools)
	t.mu.Lock()
	t.tools = cp
	t.mu.Unlock()
}

// List is the table's tools as a snapshot.
func (t *Table) List() []core.Tool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	cp := make([]core.Tool, len(t.tools))
	copy(cp, t.tools)
	return cp
}

// Specs is the wire's projection: the table's name, description, and
// schema per tool, in the table's order.
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

// Resolve is the exec's end: the table first, the inner exec second
// (the loop's own directExec, whose unknown-tool voice bounds the
// rest). List it first — innermost, first-listed is innermost — so the
// chain's participants (the allow-list, the bound) still bound what the
// table serves.
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

// Carry is the request's end: the table's specs stamped into the
// request's tools array before delegation, per call. The loop's own
// (startup) list is a bootstrap for the chain's lookup; the array on
// the wire is the table's, at the moment of the call — a swap takes
// effect on the next call, and an in-flight request keeps the list it
// was stamped with.
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
