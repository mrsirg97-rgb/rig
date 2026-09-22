package scheduler

import (
	"github.com/mrsirg97-rgb/rig/store"
	schedddl "github.com/mrsirg97-rgb/rig/store/scheduler/ddl"
)

const SchemaVersion = 5

func Statements() []string { return schedddl.Statements() }

type DB = store.DB
