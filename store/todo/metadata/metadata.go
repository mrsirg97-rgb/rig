package metadata

import (
	_ "embed"
	"strings"
)

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

// table:"tasks"
type Task struct {
	ID           string `primary:"true" alias:"name=id,nullable=false"`
	Text         string `alias:"name=text,nullable=false"`
	Status       string `alias:"name=status,nullable=false"`
	Pos          int64  `alias:"name=pos,nullable=false"`
	CreatedSeq   int64  `alias:"name=created_seq,nullable=false"`
	CreatedEvent Event  `link:"from=Event,on=seq,many=false"`
	UpdatedSeq   int64  `alias:"name=updated_seq,nullable=false"`
	UpdatedEvent Event  `link:"from=Event,on=seq,many=false"`
}

// table:"task_deps"
type TaskDep struct {
	TaskID       string `primary:"true" alias:"name=task_id,nullable=false"`
	Task         Task   `link:"from=Task,on=id,many=false"`
	DependsOn    string `primary:"true" alias:"name=depends_on,nullable=false"`
	Depended     Task   `link:"from=Task,on=id,many=false"`
	CreatedSeq   int64  `alias:"name=created_seq,nullable=false"`
	CreatedEvent Event  `link:"from=Event,on=seq,many=false"`
}

//go:embed extra.sql
var extraSQL []byte

func ExtraStatements() []string {
	var out []string
	for _, stmt := range strings.Split(string(extraSQL), ";") {
		var lines []string
		for _, l := range strings.Split(stmt, "\n") {
			if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "--") {
				continue
			}
			lines = append(lines, l)
		}
		if len(lines) > 0 {
			out = append(out, strings.Join(lines, "\n"))
		}
	}
	return out
}
