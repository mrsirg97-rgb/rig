// Hand-written metadata for the scheduler store: the containers
// SPEC_STATE's "### scheduler" section fixes — events (the spine), jobs
// (the disposable projection), runs (the structured run records; the
// spec's deviation that makes runs reads a chain read), meta (versions).
// Source of truth; domain and ddl are generated from it, never typed by
// hand. Nullable columns are pointers. Every link pairs with a plain
// alias sharing its column (lift's association shape) so the FK survives
// into the generated INSERT. CHECK-style invariants are not expressible
// here and are enforced in Go.
package metadata

// table:"meta"
type Meta struct {
	Key   string `primary:"true" alias:"name=key,nullable=false"`
	Value string `alias:"name=value,nullable=false"`
}

// table:"events"
//
// The spine. Seq is minted as max+1 over the fold inside the caller's
// transaction: strictly increasing by construction, append-only.
type Event struct {
	Seq     int64   `primary:"true" alias:"name=seq,nullable=false"`
	Ts      string  `alias:"name=ts,nullable=false"`
	Op      string  `alias:"name=op,nullable=false"`
	Args    string  `alias:"name=args,nullable=false"`
	Session *string `alias:"name=session,nullable=true"`
}

// table:"jobs"
//
// The projection, rebuilt from the log inside every transaction and never
// trusted. Removed jobs stay as tombstones (state='removed'): ids are
// minted forward over them and names are never reused. created_seq and
// updated_seq pair their links, exactly as the todo tasks shape.
type Job struct {
	ID           string  `primary:"true" alias:"name=id,nullable=false"`
	Name         string  `alias:"name=name,nullable=false"`
	Prompt       string  `alias:"name=prompt,nullable=false"`
	Cron         string  `alias:"name=cron,nullable=false"`
	At           *string `alias:"name=at,nullable=true"`
	Cwd          string  `alias:"name=cwd,nullable=false"`
	Model        string  `alias:"name=model,nullable=false"`
	Busy         string  `alias:"name=busy,nullable=false"`
	State        string  `alias:"name=state,nullable=false"`
	LastStatus   *string `alias:"name=last_status,nullable=true"`
	LastTs       *string `alias:"name=last_ts,nullable=true"`
	LastExit     *int64  `alias:"name=last_exit,nullable=true"`
	CreatedSeq   int64   `alias:"name=created_seq,nullable=false"`
	CreatedEvent Event   `link:"from=Event,on=seq,many=false"`
	UpdatedSeq   int64   `alias:"name=updated_seq,nullable=false"`
	UpdatedEvent Event   `link:"from=Event,on=seq,many=false"`
}

// table:"runs"
//
// Structured run records (SPEC_STATE's deviation from run-events-as-the-
// only-record). Runs reads become a chain read over this container and
// survive compaction, which an event-args-only shape would have dropped.
// The full record shape is kept: status, exit, duration, reason, log path.
// Seq pairs the record with its run event (the event's seq).
type Run struct {
	Seq        int64   `primary:"true" alias:"name=seq,nullable=false"`
	JobID      string  `alias:"name=job_id,nullable=false"`
	RunJob     Job     `link:"from=Job,on=id,many=false"`
	StartedAt  string  `alias:"name=started_at,nullable=false"`
	EndedAt    string  `alias:"name=ended_at,nullable=false"`
	Status     string  `alias:"name=status,nullable=false"`
	Exit       *int64  `alias:"name=exit,nullable=true"`
	DurationMs *int64  `alias:"name=duration_ms,nullable=true"`
	Reason     *string `alias:"name=reason,nullable=true"`
	LogPath    *string `alias:"name=log_path,nullable=true"`
}
