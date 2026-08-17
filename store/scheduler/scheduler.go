// Package scheduler is the background-jobs store: Go over the generated
// substrate. SPEC_STATE's "### scheduler" section is the spec.
//
// The event log is the spine. jobs is a replayable projection, rebuilt
// from the log inside every transaction and never trusted. Removed jobs
// stay as tombstones so ids and names are never reused and remove
// survives compaction. Runs are structured records in their own
// container (SPEC_STATE's deviation): runs reads are chain reads over it
// and run history survives compaction, which an event-args-only shape
// would have dropped. Crontab is the scheduling truth: tagged lines,
// surgical rewrites, foreign lines byte-identical, written before the
// store commit; drift is surfaced in list.
package scheduler

import (
	"github.com/mrsirg97-rgb/rig/store"
	schedddl "github.com/mrsirg97-rgb/rig/store/scheduler/ddl"
)

// SchemaVersion is applied at Open; mismatches are refused loudly.
const SchemaVersion = 1

// DDL: the generated statements alone (the drift diff's left side).
func DDL() []string { return schedddl.Statements() }

// Statements: the store's schema in application order — the generated DDL
// alone. CHECK-style invariants are not expressible in the camera and are
// enforced in Go; the schema carries no indexes, so there is no extra.sql
// here.
func Statements() []string { return schedddl.Statements() }

// DB is the store handle; naming it here keeps call sites scoped to this
// store.
type DB = store.DB
