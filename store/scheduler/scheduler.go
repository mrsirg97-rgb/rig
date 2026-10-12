package scheduler

import (
	"github.com/mrsirg97-rgb/rig/v3/store"
	schedddl "github.com/mrsirg97-rgb/rig/v3/store/scheduler/ddl"
)

const SchemaVersion = 7

func Statements() []string { return schedddl.Statements() }

type DB = store.DB
