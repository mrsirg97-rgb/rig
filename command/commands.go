package command

import "github.com/mrsirg97-rgb/rig/core"

// All is the standard set: the seven commands, one file each. The root
// registers the set with one line — a new command is one file in this
// package and one line there, zero loop lines.
func All() []core.Command {
	return []core.Command{
		compactCmd{},
		newCmd{},
		sessionsCmd{},
		modelsCmd{},
		steerCmd{},
		toolCmd{name: "todo", parse: todoArgs},
		toolCmd{name: "scheduler", parse: schedulerArgs},
	}
}
