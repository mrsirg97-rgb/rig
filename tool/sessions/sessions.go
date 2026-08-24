package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/scope"
	"github.com/mrsirg97-rgb/rig/store/state"
)

const (
	defaultN = 10
	maxN     = state.ListCap
)

const schemaJSON = `{
	"type": "object",
	"required": ["action"],
	"properties": {
		"action": {
			"enum": ["list", "summary"]
		},
		"project": {
			"type": "string"
		},
		"n": {
			"type": "integer"
		}
	}
}`

const description = "the session store, read-only: list sessions, or summary the vitals (models, faults, cache " +
	"ratio). Guidelines: this workspace by default, or project and n. Reply: a line per session, or " +
	"the vitals."

type adapter struct {
	home string
	cwd  string
}

func New(home, cwd string) core.Tool { return adapter{home: home, cwd: cwd} }

func (a adapter) Name() string        { return "sessions" }
func (a adapter) Description() string { return description }
func (a adapter) Schema() json.RawMessage {
	return json.RawMessage(schemaJSON)
}

type given struct {
	Action  string  `json:"action"`
	Project *string `json:"project"`
	N       *int    `json:"n"`
}

func (a adapter) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var g given
	if err := json.Unmarshal(args, &g); err != nil {
		return "", fmt.Errorf("sessions: %v", err)
	}
	project := a.cwd
	if g.Project != nil && *g.Project != "" {
		project = *g.Project
		if abs, err := filepath.Abs(project); err == nil {
			project = abs
		}
	}
	n := defaultN
	if g.N != nil {
		if *g.N < 1 || *g.N > maxN {
			return "", fmt.Errorf("sessions: n must be within 1..50, got %d", *g.N)
		}
		n = *g.N
	}
	switch g.Action {
	case "":
		return "", fmt.Errorf("sessions: action required")
	case "list", "summary":
	default:
		return "", fmt.Errorf("sessions: unknown action %q", g.Action)
	}
	path := state.StorePath(a.home, project)
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyReply(project), nil
		}
		return "", fmt.Errorf("sessions: %v", err)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("sessions: %s is a directory, not a state file", path)
	}
	db, _, _, err := store.Open(path, state.Statements(), state.SchemaVersion)
	if err != nil {
		return "", fmt.Errorf("sessions: %v", err)
	}
	defer db.DB.Close()
	if g.Action == "list" {
		return a.list(ctx, db, project, n)
	}
	return a.summary(ctx, db, project, n)
}

func (a adapter) list(ctx context.Context, db store.DB, project string, n int) (string, error) {
	rows, err := state.ListSessions(ctx, db, n)
	if err != nil {
		return "", fmt.Errorf("sessions: %v", err)
	}
	if len(rows) == 0 {
		return emptyReply(project), nil
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s  started %s  model %s  version %s  turns %d  faults %d\n",
			shortID(r.ID), r.Started.UTC().Format(time.RFC3339), r.Model, r.Version, r.Turns, r.Faults)
	}
	return b.String(), nil
}

func (a adapter) summary(ctx context.Context, db store.DB, project string, n int) (string, error) {
	rows, err := state.ListSessions(ctx, db, n)
	if err != nil {
		return "", fmt.Errorf("sessions: %v", err)
	}
	if len(rows) == 0 {
		return emptyReply(project), nil
	}
	turns := 0
	faultCount := 0
	models := map[string]bool{}
	var prompt, cacheRead int64
	var lastFault *state.FaultRow
	for _, r := range rows {
		turns += r.Turns
		faultCount += r.Faults
		models[r.Model+" "+r.Version] = true
		usage, err := state.SessionUsage(ctx, db, r.ID)
		if err != nil {
			return "", fmt.Errorf("sessions: %v", err)
		}
		for _, u := range usage {
			prompt += u.Prompt
			cacheRead += u.CacheRead
		}
		faults, err := state.SessionFaults(ctx, db, r.ID)
		if err != nil {
			return "", fmt.Errorf("sessions: %v", err)
		}
		if len(faults) > 0 {
			f := faults[0]
			if lastFault == nil || f.At.After(lastFault.At) || (f.At.Equal(lastFault.At) && f.Seq > lastFault.Seq) {
				lastFault = &f
			}
		}
	}
	modelLines := make([]string, 0, len(models))
	for m := range models {
		modelLines = append(modelLines, m)
	}
	sort.Strings(modelLines)
	pct := 0
	if prompt > 0 {
		pct = int(cacheRead * 100 / prompt)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d session%s, %d turn%s\n",
		scope.Label(project), len(rows), plural(len(rows)), turns, plural(turns))
	fmt.Fprintf(&b, "models: %s\n", strings.Join(modelLines, ", "))
	if faultCount == 0 {
		b.WriteString("faults: 0\n")
	} else {
		fmt.Fprintf(&b, "faults: %d — last: %s\n", faultCount, firstLine(lastFault.Message))
	}
	fmt.Fprintf(&b, "cache ratio: %d%% (cache_read %d / prompt %d)", pct, cacheRead, prompt)
	return b.String(), nil
}

func emptyReply(project string) string {
	return "(no sessions in " + scope.Label(project) + ")"
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
