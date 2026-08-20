package metadata

// table:"meta"
type Meta struct {
	Key   string `primary:"true" alias:"name=key,nullable=false"`
	Value string `alias:"name=value,nullable=false"`
}

// table:"events"
type Event struct {
	Seq     int64   `primary:"true" alias:"name=seq,nullable=false"`
	Ts      string  `alias:"name=ts,nullable=false"`
	Op      string  `alias:"name=op,nullable=false"`
	Args    string  `alias:"name=args,nullable=false"`
	Session *string `alias:"name=session,nullable=true"`
}

// table:"jobs"
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
