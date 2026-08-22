package command

import (
	"context"
	"fmt"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/models"
)

type Steerer interface {
	Steer(text string) bool
	Interrupt() bool
	ClearSlot()
	LiveTurn() bool
}

type SessionRow struct {
	ID      string
	Started time.Time
	Exit    string
	Turns   int
	Current bool
}

type Env struct {
	Session func() *core.Session

	Steer Steerer

	Compact       func(ctx context.Context) (core.Compacted, bool, error)
	NewSession    func(ctx context.Context) (string, error)
	SessionList   func(ctx context.Context) ([]SessionRow, error)
	SessionShow   func(ctx context.Context, id string) (string, error)
	SessionResume func(ctx context.Context, id string) error
	Models        func() models.Table
	ActiveModel   func() string
	// SwitchModel switches the active model; the string is the switch's
	// note (SPEC_MODES 1, amended: the effort dial's reset when the new
	// row does not name the level), appended to the reply — empty when
	// the switch has nothing to say.
	SwitchModel func(ctx context.Context, id string) (string, error)
	Effort      func() string   // the active effort ("" = the server default, SPEC_MODES 1)
	Efforts     func() []string // the active row's available levels (empty = the dial is off)
	SetEffort   func(ctx context.Context, level string) error
	Role        func() string // the active stance ("" = default)
	SetRole     func(ctx context.Context, name string) error
	// Approve is the tool-approval dial (SPEC_MODES 4): auto or manual.
	// SetApprove refuses manual when the frontend cannot ask (the root's
	// door rule) — the refusal is the reply.
	Approve    func() string
	SetApprove func(ctx context.Context, mode string) error
	Tools      map[string]core.Tool

	Plugins    func() []PluginInfo
	Reload     func(ctx context.Context) (string, error)
	PluginsDir string
}

func EnvOf(env any) (*Env, error) {
	e, ok := env.(*Env)
	if !ok {
		return nil, fmt.Errorf("command: env is *command.Env (got %T)", env)
	}
	return e, nil
}

func liveTurn(e *Env) bool {
	return e.Steer != nil && e.Steer.LiveTurn()
}
