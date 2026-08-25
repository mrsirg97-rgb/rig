# tool/scheduler

## What it is

Adapts the scheduler store to rig's tool seam: the description,
guidelines, schema, and runtime voices; the Exec mapping onto the store's
verbs with the threaded session attribution (the adapter consumes opened
seams).

## What it includes

- `Tool` — a `core.Tool` over the scheduler store's verbs
  (list/create/update/pause/resume/remove/runs), consuming the opened
  `sched.DB` (the one `global.sqlite`), `sched.Crontab`, the runner
  command, and the default model.

## How it is consumed

- Registered at the root as a native tool (wired with the opened store,
  `RealCrontab`, and `self+" run-job"`).

## Gotchas

- `create` carries a job name, prompt, and cron; the store validates the
  cron it gets (the adapter parses, the store teaches).
- The schema carries no `scope` (SPEC_STATE's one-store scheduler): `cwd`
  is the job's own field, ids are one sequence, `name` unique store-wide.
