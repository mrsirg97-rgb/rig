package command

import "github.com/mrsirg97-rgb/rig/v3/core"

func All() []core.Command {
	return []core.Command{
		compactCmd{},
		newCmd{},
		projectCmd{},
		sessionsCmd{},
		&modelsCmd{},
		steerCmd{},
		toolCmd{name: "todo", parse: todoArgs},
		toolCmd{name: "scheduler", parse: schedulerArgs},
		pluginsCmd{},
		&decisionCmd{},
		remCmd{},
		&effortCmd{},
		roleCmd{},
		approveCmd{},
		swarmCmd{},
		themeCmd{},
		webtoolsCmd{},
	}
}

type Sub struct {
	Name string
	Desc string
}

type Subber interface {
	Sub() []Sub
}
