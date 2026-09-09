# tool/scheduler

## What it is

Adapts the scheduler store to rig's tool seam: the description,
guidelines, schema, and runtime voices; the Exec mapping onto the store's
verbs with the threaded session attribution (the adapter consumes opened
seams).

## What it includes

- `Tool`: a `core.Tool` over the scheduler store's verbs
  (list/create/update/pause/resume/remove/runs), consuming the opened
  `sched.DB` (the one `global.sqlite`), `sched.Crontab`, the runner
  command, and the fleet's model (the create default the root supplies;
  the tool carries no worker default of its own).

## How it is consumed

- Registered at the root as a native tool only while a fleet is
  configured (wired with the opened store, `RealCrontab`,
  `self+" run-job"`, and the fleet's model).

## Gotchas

- `create` carries a job name, prompt, and cron: the store validates the
  cron it gets (the adapter parses, the store teaches).
- `create` and `update` run the one cwd rule in `pathguard` (shared with
  the delegate tool): a cwd outside the session's cwd or the rig home
  refuses at the boundary, and the runner rechecks the stored cwd at fire
  time.
- The schema carries no `scope` (SPEC_STATE's one-store scheduler): `cwd`
  is the job's own field, ids are one sequence, `name` unique store-wide.
