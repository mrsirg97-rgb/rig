// Hand-written metadata for the decision store: the containers
// SPEC_DECISION fixes — decisions (the row per decision), meta
// (versions), and trainings (2.13.0: one row per training run). Source
// of truth; domain and ddl are generated from it, never typed by hand.
// Nullable columns are pointers. The question and state columns are
// JSON text the store serializes and reads; confidence is null for a
// rule decision, which does not estimate. Ids are minted max+1 inside
// the caller's transaction. A training row's three report columns are
// the held-out reports as JSON text; the incumbent's is null where no
// unit was named, and the row lands only when the reports exist.
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

// table:"decisions"
type Decision struct {
	ID             int64    `primary:"true" alias:"name=id,nullable=false"`
	Scope          string   `alias:"name=scope,nullable=false"`
	Site           string   `alias:"name=site,nullable=false"`
	State          string   `alias:"name=state,nullable=false"`
	Question       string   `alias:"name=question,nullable=false"`
	Answer         string   `alias:"name=answer,nullable=false"`
	Confidence     *float64 `alias:"name=confidence,nullable=true"`
	Decider        string   `alias:"name=decider,nullable=false"`
	Session        *string  `alias:"name=session,nullable=true"`
	Status         string   `alias:"name=status,nullable=false"`
	Unsure         bool     `alias:"name=unsure,nullable=false"`
	Reviewer       *string  `alias:"name=reviewer,nullable=true"`
	ReviewerAnswer *string  `alias:"name=reviewer_answer,nullable=true"`
	Outcome        *string  `alias:"name=outcome,nullable=true"`
	Ts             string   `alias:"name=ts,nullable=false"`
}

// table:"trainings"
type Training struct {
	ID         int64   `primary:"true" alias:"name=id,nullable=false"`
	Scope      string  `alias:"name=scope,nullable=false"`
	Trainer    string  `alias:"name=trainer,nullable=false"`
	Rows       int64   `alias:"name=rows,nullable=false"`
	Skipped    int64   `alias:"name=skipped,nullable=false"`
	TrainRows  int64   `alias:"name=train_rows,nullable=false"`
	HeldRows   int64   `alias:"name=held_rows,nullable=false"`
	RunDir     string  `alias:"name=run_dir,nullable=false"`
	Checkpoint string  `alias:"name=checkpoint,nullable=false"`
	Constant   string  `alias:"name=constant,nullable=false"`
	Incumbent  *string `alias:"name=incumbent,nullable=true"`
	Candidate  string  `alias:"name=candidate,nullable=false"`
	Promoted   bool    `alias:"name=promoted,nullable=false"`
	Ts         string  `alias:"name=ts,nullable=false"`
}

// extra.sql — what the DDL camera cannot emit (SPEC_DECISION): the
// pending drain reads by status, the learning reads by scope and time.
//
//go:embed extra.sql
var extraSQL []byte

// ExtraStatements: the embedded extra.sql as individual statements —
// the status and scope indexes, what the camera cannot emit. Comment-only
// fragments drop; order is preserved.
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
