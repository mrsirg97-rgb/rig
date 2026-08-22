package rig

import (
	"fmt"
	"sort"

	"github.com/mrsirg97-rgb/rig/core"
)

type Kernel struct {
	Provider   core.Provider
	Frontend   core.Frontend
	Policy     core.ContextPolicy
	Tools      []core.Tool
	Middleware []core.ToolMiddleware

	Session *core.Session

	Commands []core.Command

	Concurrent func(call core.ToolCall) bool

	Parallel int
}

type Option func(*Kernel)

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

func WithProvider(p core.Provider) Option {
	return func(k *Kernel) { k.Provider = p }
}

func WithFrontend(f core.Frontend) Option {
	return func(k *Kernel) { k.Frontend = f }
}

func WithPolicy(p core.ContextPolicy) Option {
	return func(k *Kernel) { k.Policy = p }
}

func WithTools(tools ...core.Tool) Option {
	return func(k *Kernel) { k.Tools = append(k.Tools, tools...) }
}

func WithCommands(cmds ...core.Command) Option {
	return func(k *Kernel) { k.Commands = append(k.Commands, cmds...) }
}

func WithMiddleware(mw ...core.ToolMiddleware) Option {
	return func(k *Kernel) { k.Middleware = append(k.Middleware, mw...) }
}

func WithConcurrent(pred func(call core.ToolCall) bool) Option {
	return func(k *Kernel) { k.Concurrent = pred }
}

func WithParallel(n int) Option {
	return func(k *Kernel) { k.Parallel = n }
}

func (k *Kernel) SortedToolNames() []string {
	names := make([]string, 0, len(k.Tools))
	for _, t := range k.Tools {
		names = append(names, t.Name())
	}
	sort.Strings(names)
	return names
}
