// Package rig is the composition root of the dependency bag. Every
// dependency is explicit in the New call; swapping happens at registration
// with zero consumer changes. Fields are written through options only.
package rig

import (
	"fmt"
	"sort"

	"github.com/mrsirg97-rgb/rig/core"
)

// Kernel carries the loop's dependencies: interfaces and slices only, so
// the loop never names a concrete type.
type Kernel struct {
	Provider   core.Provider
	Frontend   core.Frontend
	Policy     core.ContextPolicy
	Tools      []core.Tool
	Middleware []core.ToolMiddleware

	// Session is owned by the wiring side: persistence (Save/Load) hangs
	// off it, and Run borrows it for the turn. Nil Run starts a fresh
	// in-memory session.
	Session *core.Session

	// Commands is the user-command registry (SPEC_COMMANDS 1): the loop
	// ignores it — dispatch is the Frontend's business, exactly as the
	// steering slot is.
	Commands []core.Command
}

// Option configures the Kernel at construction.
type Option func(*Kernel)

// New assembles the kernel from options. Duplicate tool names or command
// names are a wiring error and panic at startup, loud and early.
func New(opts ...Option) *Kernel {
	k := &Kernel{}
	for _, opt := range opts {
		opt(k)
	}
	seen := map[string]int{}
	for i, t := range k.Tools {
		if j, dup := seen[t.Name()]; dup {
			panic(fmt.Sprintf("rig: duplicate tool name %q (positions %d and %d)", t.Name(), j, i))
		}
		seen[t.Name()] = i
	}
	seenCmd := map[string]int{}
	for i, c := range k.Commands {
		if j, dup := seenCmd[c.Name()]; dup {
			panic(fmt.Sprintf("rig: duplicate command name %q (positions %d and %d)", c.Name(), j, i))
		}
		seenCmd[c.Name()] = i
	}
	return k
}

// WithProvider registers the model. Required: the loop fails loudly
// without one.
func WithProvider(p core.Provider) Option {
	return func(k *Kernel) { k.Provider = p }
}

// WithFrontend registers the human-facing seam. Required.
func WithFrontend(f core.Frontend) Option {
	return func(k *Kernel) { k.Frontend = f }
}

// WithPolicy registers prompt assembly. Required.
func WithPolicy(p core.ContextPolicy) Option {
	return func(k *Kernel) { k.Policy = p }
}

// WithTools registers executable capabilities. Zero tools is a valid
// boundary: the model then answers in plain text only.
func WithTools(tools ...core.Tool) Option {
	return func(k *Kernel) { k.Tools = append(k.Tools, tools...) }
}

// WithCommands registers the user commands (SPEC_COMMANDS 1): the human's
// verbs, dispatched Frontend-side by prefix before Input returns to the
// loop. Zero commands is the compat boundary: a kernel without them is
// byte-identical (10), the loop does not read the registry.
func WithCommands(cmds ...core.Command) Option {
	return func(k *Kernel) { k.Commands = append(k.Commands, cmds...) }
}

// WithMiddleware registers the execution chain in listed order;
// first-listed composes innermost.
func WithMiddleware(mw ...core.ToolMiddleware) Option {
	return func(k *Kernel) { k.Middleware = append(k.Middleware, mw...) }
}

// SortedToolNames exposes registered tool names in stable order; useful in
// diagnostics and tests.
func (k *Kernel) SortedToolNames() []string {
	names := make([]string, 0, len(k.Tools))
	for _, t := range k.Tools {
		names = append(names, t.Name())
	}
	sort.Strings(names)
	return names
}
