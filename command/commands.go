package command

import "github.com/mrsirg97-rgb/rig/core"

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

type Sub struct {
	Name string
	Desc string
}

type Subber interface {
	Sub() []Sub
}
