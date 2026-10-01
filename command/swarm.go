package command

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

const swarmUsage = "swarm start [role=worker|reviewer] [model=<id>] [budget=<dollars>] | swarm stop | bare swarm lists"

type swarmCmd struct{}

func (swarmCmd) Name() string { return "swarm" }

func (swarmCmd) Description() string {
	return "the drain workers: one per free slot on the resident model, growing as slots free (swarm start [role=worker|reviewer] [model=<id>] [budget=<dollars>], bare swarm lists, swarm stop ends)"
}

func (swarmCmd) Sub() []Sub {
	return []Sub{
		{Name: "start", Desc: "start the drain workers (one per free slot)"},
		{Name: "stop", Desc: "end the swarm"},
	}
}

func (swarmCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return swarmList(e), nil
	}
	if _, err := strconv.Atoi(fields[0]); err == nil {
		return "", fmt.Errorf("swarm: a count is not taken: the free slots are the count (the fleet is the resident model)")
	}
	if fields[0] == "stop" {
		if len(fields) != 1 {
			return "", errors.New("swarm: stop takes no args (swarm stop)")
		}
		if e.Swarm == nil {
			return "", errors.New("swarm: no swarm running")
		}
		return e.Swarm.Stop()
	}
	if fields[0] != "start" {
		return "", fmt.Errorf("swarm: unknown token %q (%s)", fields[0], swarmUsage)
	}
	in := SwarmStart{Role: "worker"}
	roleSeen, modelSeen, budgetSeen := false, false, false
	for _, f := range fields[1:] {
		switch {
		case strings.HasPrefix(f, "role="):
			if roleSeen {
				return "", errors.New("swarm: role given twice (" + swarmUsage + ")")
			}
			roleSeen = true
			in.Role = strings.TrimPrefix(f, "role=")
			if in.Role == "" {
				return "", errors.New("swarm: role needs a value (role=worker|reviewer)")
			}
		case strings.HasPrefix(f, "model="):
			if modelSeen {
				return "", errors.New("swarm: model given twice (" + swarmUsage + ")")
			}
			modelSeen = true
			in.Model = strings.TrimPrefix(f, "model=")
			if in.Model == "" {
				return "", errors.New("swarm: model needs an id (model=<id>)")
			}
		case strings.HasPrefix(f, "budget="):
			if budgetSeen {
				return "", errors.New("swarm: budget given twice (" + swarmUsage + ")")
			}
			budgetSeen = true
			raw := strings.TrimPrefix(f, "budget=")
			if raw == "" {
				return "", errors.New("swarm: budget needs a dollar amount (budget=<dollars>)")
			}
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil || v < 0 {
				return "", fmt.Errorf("swarm: budget %q: expected a non-negative dollar amount", raw)
			}
			in.Budget = v
		default:
			return "", fmt.Errorf("swarm: unknown token %q (%s)", f, swarmUsage)
		}
	}
	if e.Swarm == nil {
		return "", errors.New("swarm: no swarm seam (the root did not wire it)")
	}
	if e.Session != nil {
		if s := e.Session(); s != nil {
			ctx = core.WithSession(ctx, s)
		}
	}
	return e.Swarm.Start(ctx, in)
}

func swarmList(e *Env) string {
	if e.Swarm == nil {
		return "swarm: no workers"
	}
	rows := e.Swarm.List()
	if len(rows) == 0 {
		return "swarm: no workers"
	}
	lines := make([]string, len(rows))
	for i, r := range rows {
		activity := "exited"
		if r.State == "running" {
			activity = "task none · heartbeat —"
			if r.Task != "" {
				activity = "task " + r.Task + " · heartbeat " + heartbeatAge(r.Heartbeat)
			} else if !r.Heartbeat.IsZero() {
				activity = "task none · heartbeat " + heartbeatAge(r.Heartbeat)
			}
		}
		lines[i] = fmt.Sprintf("w%d %s %s · %s · done %d failed %d", r.ID, r.Role, r.Model, activity, r.Done, r.Failed)
	}
	return strings.Join(lines, "\n")
}

func heartbeatAge(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return fmt.Sprintf("%s ago", time.Since(t).Truncate(time.Second))
}
