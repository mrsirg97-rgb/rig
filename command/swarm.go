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

const swarmUsage = "swarm start <count> [role=worker|reviewer] [model=<id>] [budget=<dollars>] | swarm stop | bare swarm lists"

type swarmCmd struct{}

func (swarmCmd) Name() string { return "swarm" }

func (swarmCmd) Description() string {
	return "the drain pair: one router on the session's queue hands each ready task to an idle worker (swarm start <count> [role=worker|reviewer] [model=<id>] [budget=<dollars>], bare swarm lists, swarm stop ends)"
}

func (swarmCmd) Sub() []Sub {
	return []Sub{
		{Name: "start", Desc: "start N workers (swarm start 3)"},
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
		return "", fmt.Errorf("swarm: the count rides start (%s)", swarmUsage)
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
	if len(fields) < 2 {
		return "", fmt.Errorf("swarm: start takes a count (swarm start <count> [role=…] [model=…] [budget=…])")
	}
	count, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", fmt.Errorf("swarm: start takes a count (swarm start <count> [role=…] [model=…] [budget=…])")
	}
	in.Count = count
	roleSeen, modelSeen, budgetSeen := false, false, false
	for _, f := range fields[2:] {
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
	running := 0
	for _, r := range rows {
		if r.State == "running" {
			running++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s \u00b7 %d running", plural(len(rows), "worker"), running)
	for _, r := range rows {
		mark, activity := markDone, "exited"
		if r.State == "running" {
			mark = markActive
			activity = "task none \u00b7 heartbeat \u2014"
			if r.Task != "" {
				activity = "task " + r.Task + " \u00b7 heartbeat " + heartbeatAge(r.Heartbeat)
			} else if !r.Heartbeat.IsZero() {
				activity = "task none \u00b7 heartbeat " + heartbeatAge(r.Heartbeat)
			}
		}
		b.WriteString("\n" + row(fmt.Sprintf("w%d", r.ID), 0, mark, r.Role+" "+r.Model, activity,
			fmt.Sprintf("done %d failed %d", r.Done, r.Failed)))
	}
	return b.String()
}

func heartbeatAge(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return fmt.Sprintf("%s ago", time.Since(t).Truncate(time.Second))
}
