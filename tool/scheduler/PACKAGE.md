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
  verb over the scheduler store: `Create(ctx, in CreateInput)` and
  `Update(ctx, id, in UpdateInput)` — the job's nine fields go in one
  input struct, the store's `CreateInput` shape, because six of them are
  strings and a caller must not transpose a schedule for a workspace —
  and `List(ctx)`, `Show(ctx, id)`, `Pause(ctx, id)`, `Resume(ctx, id)`,
  `Remove(ctx, id)`, `Runs(ctx, id, n)` and `Repair(ctx, id)`, small
  enough to stay positional with the job named first. The required-argument
  refusals, the command-job model gate and the `pathguard` workspace rule
  live in the verb they belong to, so a Go caller and the model hit the
  same checks. `New` returns the interface; the struct is unexported. The
  tool consumes the opened `sched.DB` (the one `global.sqlite`),
  `sched.Crontab`, the runner command, and the fleet's model (the fire-time
  default the description names; the tool carries no worker default of its
  own). Create takes one job whole — the schedule (a five-field cron, or
  `once` with `at`), the work (a prompt for a worker, or a command line
  for no model) and the limits — and a zero value leaves each field to
  the store's default; update takes the same fields as they change, the
  job named by the id on the verb itself, as every other verb names its
  job.

## How it is consumed

- Registered at the root as a native tool only while a fleet is
  configured (wired with the opened store, `RealCrontab`,
  `self+" run-job"`, and the fleet's model).

## Gotchas

- `remove` is the operator's verb (2.14.1, SPEC_WORKERS 7): it rides the
  registry entry's `operator` key, so a delegated worker's menu omits it
  and `policy/operator` refuses it before the tool runs. The switch here
  is untouched; a session without the delegate marker keeps every verb.
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
- `show` reads one job by id: the store prints the same block `list`
  prints for it plus the job's last run line, so a caller holding a `jN`
  from `list` or a `runs` reply reads its row without the board; an
  empty id refuses in the verb's own voice
  (`scheduler: show requires 'id' (jN)`), an unknown one names it and
  points at `list`.
- `repair` takes an id or none: the id is optional on the schema (none
  repairs every drifting job), and the verb is a crontab write only —
  no event, no state change — with the drift it fixed named verbatim
  (`'jN' is in sync` when nothing drifts, `nothing to repair` for a
  removed or done job, `nothing drifted` when the walk finds none).
