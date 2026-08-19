package command

import "github.com/mrsirg97-rgb/rig/core"

// All is the standard set: the eight commands, one file each. The root
// registers the set with one line — a new command is one file in this
// package and one line there, zero loop lines.
func All() []core.Command {
	return []core.Command{
		compactCmd{},
		newCmd{},
		sessionsCmd{},
		&modelsCmd{},
		steerCmd{},
		toolCmd{name: "todo", parse: todoArgs},
		toolCmd{name: "scheduler", parse: schedulerArgs},
		pluginsCmd{},
	}
}

// Sub is a command's argument hint (SPEC_TUI 9, amended): a name the
// operator can type after the command's name, and its description —
// the TUI's completion menu's row. Frontend-only: the CLI's dispatch
// ignores it.
type Sub struct {
	Name string
	Desc string
}

// Subber is the optional argument-hints door (SPEC_TUI 9, amended):
// the command's Sub() feeds the TUI's completion menu. A command
// without it keeps the TUI's description ghost.
type Subber interface {
	Sub() []Sub
}
