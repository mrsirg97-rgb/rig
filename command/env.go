// Package command is the user-command leaf (SPEC_COMMANDS): the prefix
// rule, the Env the root builds, and the standard set — one file per
// command, testable with fakes: no kernel, no stores, no provider.
// Stdlib plus core and models, nothing else: the leaf depends on core and
// models; the root depends on the leaf.
package command

import (
	"context"
	"fmt"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/models"
)

// Steerer is the frontend-owned seam (decision 2): the slot, the
// interrupt handle, and the liveness fact — the Frontend's contract
// (7's), filled by the dispatcher in its WithCommands, not the root's.
type Steerer interface {
	Steer(text string) bool // queue latest-wins; interrupt a live turn;
	// reports whether the interrupt landed
	Interrupt() bool // interrupt a live turn only; reports the same
	ClearSlot()      // drop the queued intent (a new session does not inherit it)
	LiveTurn() bool  // a turn is live right now (compact / new / sessions resume refuse on it)
}

// SessionRow is one listed session, for the sessions command (5).
type SessionRow struct {
	ID      string
	Started time.Time
	Exit    string
	Turns   int
	Current bool // the live session, marked in the list
}

// Env is the command's world, built at the root (decision 2): closures,
// not handles — the command package sees core and models and nothing
// else; no store type, no recorder, no policy, no kernel. The root's
// mutable state is read at call time, so a swap is visible with no
// re-wiring.
type Env struct {
	Session func() *core.Session // the live session (post-swap)

	// frontend-owned seam, filled by the dispatcher (decision 2): nil =
	// the steer command refuses loud, and compact / new / sessions
	// resume read LiveTurn as false (nil-safe).
	Steer Steerer

	// root-owned operations
	Compact       func(ctx context.Context) (core.Compacted, bool, error)
	NewSession    func(ctx context.Context) (string, error)
	SessionList   func(ctx context.Context) ([]SessionRow, error)
	SessionShow   func(ctx context.Context, id string) (string, error)
	SessionResume func(ctx context.Context, id string) error
	Models        func() models.Table
	ActiveModel   func() string
	SwitchModel   func(ctx context.Context, id string) error
	Tools         map[string]core.Tool // the same instances the model gets

	// Plugins is the discovery's rows, in file order: the loaded (name,
	// description, file) and the skipped (name, reason) — SPEC_PLUGINS
	// 4. Nil: no plugins seam (the root wired none).
	Plugins []PluginInfo
}

// EnvOf asserts the dispatcher's env is this package's Env (decision 2):
// the dispatcher carries what the root built without naming its type; a
// foreign type is a wiring error named where it is found.
func EnvOf(env any) (*Env, error) {
	e, ok := env.(*Env)
	if !ok {
		return nil, fmt.Errorf("command: env is *command.Env (got %T)", env)
	}
	return e, nil
}

// liveTurn is the nil-safe refusal predicate (decision 2): a Steerer that
// reports a live turn trips the compact / new / sessions resume refusal.
func liveTurn(e *Env) bool {
	return e.Steer != nil && e.Steer.LiveTurn()
}
