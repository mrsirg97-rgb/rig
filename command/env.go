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

type RemRow struct {
	ID         int64
	Kind       string
	ScopeLabel string
	CreatedAt  string
	Strength   float64
	Importance float64
	Source     string
	Superseded *int64
	Content    string
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

	SwitchModel func(ctx context.Context, id string) (string, error)
	Effort      func() string
	Efforts     func() []string
	SetEffort   func(ctx context.Context, level string) error
	Role        func() string
	SetRole     func(ctx context.Context, name string) error

	Approve    func() string
	SetApprove func(ctx context.Context, mode string) error
	Tools      map[string]core.Tool

	RemList   func(ctx context.Context, project string) ([]RemRow, error)
	RemShow   func(ctx context.Context, id int64) (RemRow, error)
	RemForget func(ctx context.Context, id int64) error
	RemLabel  func(ctx context.Context, project string) (string, error)

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
