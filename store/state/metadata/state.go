package metadata

import "time"

// table:"sessions"
type Session struct {
	ID        string     `primary:"true" alias:"name=id,nullable=false"`
	Cwd       string     `alias:"name=cwd,nullable=false"`
	Model     string     `alias:"name=model,nullable=false"`
	StartedAt time.Time  `alias:"name=started_at,nullable=false"`
	EndedAt   *time.Time `alias:"name=ended_at,nullable=true"`
	Exit      string     `alias:"name=exit,nullable=false"`
	Version   string     `alias:"name=version,nullable=false"`
}

// table:"messages"
type Message struct {
	Seq       int64     `primary:"true" alias:"name=seq,nullable=false"`
	SessionID string    `alias:"name=session_id,nullable=false"`
	Session   *Session  `link:"from=Session,on=id,many=false"`
	Role      string    `alias:"name=role,nullable=false"`
	Content   string    `alias:"name=content,nullable=false"`
	Reasoning *string   `alias:"name=reasoning,nullable=true"`
	ToolID    *string   `alias:"name=tool_id,nullable=true"`
	CreatedAt time.Time `alias:"name=created_at,nullable=false"`
}

// table:"tool_calls"
type ToolCall struct {
	ID         string     `primary:"true" alias:"name=id,nullable=false"`
	MessageSeq int64      `alias:"name=message_seq,nullable=false"`
	Message    *Message   `link:"from=Message,on=seq,many=false"`
	Name       string     `alias:"name=name,nullable=false"`
	Args       string     `alias:"name=args,nullable=false"`
	Result     *string    `alias:"name=result,nullable=true"`
	Err        *string    `alias:"name=err,nullable=true"`
	StartedAt  time.Time  `alias:"name=started_at,nullable=false"`
	EndedAt    *time.Time `alias:"name=ended_at,nullable=true"`
}

// table:"usage"
type Usage struct {
	MessageSeq int64    `primary:"true" alias:"name=message_seq,nullable=false"`
	Message    *Message `link:"from=Message,on=seq,many=false"`
	Prompt     int64    `alias:"name=prompt,nullable=false"`
	Completion int64    `alias:"name=completion,nullable=false"`
	CacheRead  int64    `alias:"name=cache_read,nullable=false"`
	CacheWrite int64    `alias:"name=cache_write,nullable=false"`
}

// table:"files"
type File struct {
	SessionID string `primary:"true" alias:"name=session_id,nullable=false"`
	Path      string `primary:"true" alias:"name=path,nullable=false"`
	Hash      string `alias:"name=hash,nullable=false"`
	Mtime     int64  `alias:"name=mtime,nullable=false"`
}

// table:"faults"
type Fault struct {
	Seq       int64     `primary:"true" alias:"name=seq,nullable=false"`
	SessionID string    `alias:"name=session_id,nullable=false"`
	Session   *Session  `link:"from=Session,on=id,many=false"`
	At        time.Time `alias:"name=at,nullable=false"`
	Message   string    `alias:"name=message,nullable=false"`
}
