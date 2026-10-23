package rig

import (
	"fmt"
	"sort"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3/core"
)

type Kernel interface {
	Seat
	Tools() []core.Tool
	Middleware() []core.ToolMiddleware
	Commands() []core.Command
	Priority() func(call core.ToolCall) int
	Parallel() int
	Engine() evt.Engine
	SortedToolNames() []string
}

type Seat interface {
	Provider() core.Provider
	Frontend() core.Frontend
	Policy() core.ContextPolicy
	Session() *core.Session
}

type kernel struct {
	Seat
	fixed      *fixedSeat
	tools      []core.Tool
	middleware []core.ToolMiddleware
	commands   []core.Command
	priority   func(call core.ToolCall) int
	parallel   int
	engine     evt.Engine
}

type fixedSeat struct {
	provider core.Provider
	frontend core.Frontend
	policy   core.ContextPolicy
	session  *core.Session
}

func (s *fixedSeat) Provider() core.Provider    { return s.provider }
func (s *fixedSeat) Frontend() core.Frontend    { return s.frontend }
func (s *fixedSeat) Policy() core.ContextPolicy { return s.policy }
func (s *fixedSeat) Session() *core.Session     { return s.session }

const DefaultParallel = 8

const (
	PriorityInput    = 90
	PriorityDelegate = 70
	PriorityWrite    = 60
	PriorityRead     = 50
	PriorityStream   = 50
	PriorityTool     = 50
	PriorityFleet    = 30
	PriorityReview   = 10
)

const (
	MemberFrontend int64 = -1
	MemberDelegate int64 = -2
	MemberGraph    int64 = -3
	MemberDecision int64 = -4
)

type Option func(*kernel)

func New(opts ...Option) Kernel {
	k := &kernel{}
	for _, opt := range opts {
		opt(k)
	}
	switch {
	case k.Seat != nil && k.fixed != nil:
		panic("rig: a kernel takes a seat or its fixed parts (WithProvider, WithFrontend, WithPolicy, WithSession), not both")
	case k.Seat == nil:
		if k.fixed == nil {
			k.fixed = &fixedSeat{}
		}
		if k.fixed.session == nil {
			k.fixed.session = core.NewSession()
		}
		k.Seat = k.fixed
	}
	k.fixed = nil
	seen := map[string]int{}
	for i, t := range k.tools {
		if j, dup := seen[t.Name()]; dup {
			panic(fmt.Sprintf("rig: duplicate tool name %q (positions %d and %d)", t.Name(), j, i))
		}
		seen[t.Name()] = i
	}
	seenCmd := map[string]int{}
	for i, c := range k.commands {
		if j, dup := seenCmd[c.Name()]; dup {
			panic(fmt.Sprintf("rig: duplicate command name %q (positions %d and %d)", c.Name(), j, i))
		}
		seenCmd[c.Name()] = i
	}
	return k
}

func (k *kernel) part() *fixedSeat {
	if k.fixed == nil {
		k.fixed = &fixedSeat{}
	}
	return k.fixed
}

func WithSeat(s Seat) Option {
	return func(k *kernel) { k.Seat = s }
}

func WithProvider(p core.Provider) Option {
	return func(k *kernel) { k.part().provider = p }
}

func WithFrontend(f core.Frontend) Option {
	return func(k *kernel) { k.part().frontend = f }
}

func WithPolicy(p core.ContextPolicy) Option {
	return func(k *kernel) { k.part().policy = p }
}

func WithSession(s *core.Session) Option {
	return func(k *kernel) { k.part().session = s }
}

func WithTools(tools ...core.Tool) Option {
	return func(k *kernel) { k.tools = append(k.tools, tools...) }
}

func WithCommands(cmds ...core.Command) Option {
	return func(k *kernel) { k.commands = append(k.commands, cmds...) }
}

func WithMiddleware(mw ...core.ToolMiddleware) Option {
	return func(k *kernel) { k.middleware = append(k.middleware, mw...) }
}

func WithPriority(pred func(call core.ToolCall) int) Option {
	return func(k *kernel) { k.priority = pred }
}

func WithParallel(n int) Option {
	return func(k *kernel) { k.parallel = n }
}

func WithEngine(e evt.Engine) Option {
	return func(k *kernel) { k.engine = e }
}

func (k *kernel) Tools() []core.Tool                     { return k.tools }
func (k *kernel) Middleware() []core.ToolMiddleware      { return k.middleware }
func (k *kernel) Commands() []core.Command               { return k.commands }
func (k *kernel) Priority() func(call core.ToolCall) int { return k.priority }
func (k *kernel) Engine() evt.Engine                     { return k.engine }

func (k *kernel) Parallel() int {
	if k.parallel <= 0 {
		return DefaultParallel
	}
	return k.parallel
}

func (k *kernel) SortedToolNames() []string {
	names := make([]string, 0, len(k.tools))
	for _, t := range k.tools {
		names = append(names, t.Name())
	}
	sort.Strings(names)
	return names
}
