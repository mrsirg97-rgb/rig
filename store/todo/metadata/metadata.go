// Hand-written metadata for the todo store: the four containers
// SPEC_STATE's "### todo" section fixes — events (the spine), tasks
// (the disposable projection), task_deps (the graph), meta (versions).
// Source of truth; domain and ddl are generated from it, never typed by
// hand. Nullable columns are pointers. Every link is paired with a plain
// alias sharing its column (lift's association shape) so the FK survives
// into the generated INSERT.
package metadata

import _ "embed"

// table:"meta"
type Meta struct {
	Key   string `primary:"true" alias:"name=key,nullable=false"`
	Value string `alias:"name=value,nullable=false"`
}

// table:"events"
//
// Seq is minted by sqlite's rowid semantics on omission: events is
// append-only and strictly increasing by construction.
type Event struct {
	Seq     int64   `primary:"true" alias:"name=seq,nullable=false"`
	Ts      string  `alias:"name=ts,nullable=false"`
	Op      string  `alias:"name=op,nullable=false"`
	Args    string  `alias:"name=args,nullable=false"`
	Session *string `alias:"name=session,nullable=true"`
}

// table:"tasks"
type Task struct {
	ID           string `primary:"true" alias:"name=id,nullable=false"`
	Text         string `alias:"name=text,nullable=false"`
	Status       string `alias:"name=status,nullable=false"`
	Pos          int    `alias:"name=pos,nullable=false"`
	CreatedSeq   int64  `alias:"name=created_seq,nullable=false"`
	CreatedEvent Event  `link:"from=Event,on=seq,many=false"`
	UpdatedSeq   int64  `alias:"name=updated_seq,nullable=false"`
	UpdatedEvent Event  `link:"from=Event,on=seq,many=false"`
}

// table:"task_deps"
//
// Composite primary (task_id + depends_on); both sides reference tasks.
type TaskDep struct {
	TaskID       string `primary:"true" alias:"name=task_id,nullable=false"`
	Task         Task   `link:"from=Task,on=id,many=false"`
	DependsOn    string `primary:"true" alias:"name=depends_on,nullable=false"`
	Depended     Task   `link:"from=Task,on=id,many=false"`
	CreatedSeq   int64  `alias:"name=created_seq,nullable=false"`
	CreatedEvent Event  `link:"from=Event,on=seq,many=false"`
}

// extra.sql — what the DDL camera cannot emit (SPEC_STATE, decisions):
// the text unique index (idempotent-create natural key) and the
// (pos, created_seq) ordering spine.
//
//go:embed extra.sql
var extraSQL []byte
