package scheduler

import (
	"github.com/mrsirg97-rgb/rig/v2/store"
	schedddl "github.com/mrsirg97-rgb/rig/v2/store/scheduler/ddl"
)

const SchemaVersion = 6

func Statements() []string { return schedddl.Statements() }

type DB = store.DB
