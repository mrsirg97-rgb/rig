# tool/scheduler

## What it is

Adapts the scheduler store to rig's tool seam: the description,
guidelines, schema, and runtime voices; the Exec mapping onto the store's
verbs with the threaded session attribution (the adapter consumes opened
seams).

## What it includes

- `Scheduler`: the tool as its interface (2.12.6): `tool.Definition`,
  `Exec` as the one JSON door — decode `action` and the fields, keep the
  wire's three-state `model` (absent / `null` / a name, read from the raw
  args because Go cannot say it otherwise), route — and one method per
  verb over the scheduler store: `Create(ctx, name, prompt, command, cron,
  at, workspace, model, timeout, budget)`, `Update(ctx, id, name, prompt,
  command, cron, at, workspace, model *string, timeout, budget)`,
  `List(ctx)`, `Pause(ctx, id)`, `Resume(ctx, id)`, `Remove(ctx, id)`,
  `Runs(ctx, id, n)` and `Repair(ctx, id)`. The required-argument
  refusals, the command-job model gate and the `pathguard` workspace rule
  live in the verb they belong to, so a Go caller and the model hit the
  same checks. `New` returns the interface; the struct is unexported. The
  tool consumes the opened `sched.DB` (the one `global.sqlite`),
  `sched.Crontab`, the runner command, and the fleet's model (the fire-time
  default the description names; the tool carries no worker default of its
  own).

## How it is consumed

- Registered at the root as a native tool only while a fleet is
  configured (wired with the opened store, `RealCrontab`,
  `self+" run-job"`, and the fleet's model).

## Gotchas

- `create` carries a job name, a prompt or a command, and a cron: the
  store validates the cron it gets (the adapter parses, the store
  teaches). A create without a model stores the unnamed job — the fire
  resolves the resident model, else the wired default — and an update's
  `model: null` clears back to it. A `command` create is the
  deterministic payload (no model, no busy policy), and the guidelines
  steer commands to scripts, never to work needing judgment.
- `timeout` (minutes) bounds one fire: the adapter passes it through on
  create and update, the store refuses it outside 1..1440 by name, and
  on update `-1` is the explicit reset to the runner default (an absent
  field means unchanged). The timeout belongs to the job, not to the
  kind: command jobs carry one too.
- `create` and `update` run the one workspace rule in `pathguard` (shared
  with the delegate tool): a workspace outside the session's workspace or
  the rig home refuses at the boundary, and the runner rechecks the stored cwd at fire
  time.
- The schema carries no `scope` (SPEC_STATE's one-store scheduler): `workspace`
  is the job's own field, ids are one sequence, `name` unique store-wide.
- `repair` takes an id or none: the id is optional on the schema (none
  repairs every drifting job), and the verb is a crontab write only —
  no event, no state change — with the drift it fixed named verbatim
  (`'jN' is in sync` when nothing drifts, `nothing to repair` for a
  removed or done job, `nothing drifted` when the walk finds none).
