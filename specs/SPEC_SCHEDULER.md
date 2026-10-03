# SPEC_SCHEDULER: the scheduler's job kinds

The scheduler store, the crontab, the fold, and the runner live in
SPEC_STATE (the scheduler store section). This spec names the one thing
a job row can be besides a task: the review chore (2.9.4).

## the `review` job kind

A job whose prompt is exactly `review` is not a task; it is the
decision reviewer's standing chore. `scheduler create` with the prompt
`review` is how the chore comes to exist — the operator's call, or the
model's own, at the operator's hour:

    scheduler create name=review-chores prompt=review cron="0 9 * * *"

The runner (`run-job jN`) treats it like every job up to the fire: the
key parses, the lock is taken (a previous run still active records a
skip), the crontab line and the job row are read, the state checks run,
the budget gate applies, and the path is canonical. At the fire it
differs in one branch: where a prompt job would build a worker argv
around its prompt, a `review` job builds the reviewer and drains the
decision store's pending rows (SPEC_DECISION, the reviewer). No worker
session is created for the prompt; the drain's own fires are ordinary
delegate fires — a `rig -p -` worker through `Delegate`, no tools, the
bounds on a fire as SPEC_DECISION names them. One run drains one fire's
worth: the rest stay pending for the next door.

## the gates, the row, the refusals

The fire's model follows the job row: a named job fires its recorded
model, an unnamed one resolves the resident model, else the runner's
`DefaultModel`. The fleet gate runs like every job's (the model must be
resident; the GPU is held by whoever is) and a refusal records a skip
that names the holder. Because the drain is sized by the model row —
the window, the reserve, the max output tokens — the runner resolves
the row the way the worker would (the table, then the `RIG_MODEL_*`
overlays), and a model with no row records a skip, named, rather than
fire unbounded. The decision store rides `RunOpts` beside the run
recorder; a runner wired without it records a skip, named.

The run is recorded like any job: one `runs` row (status, exit, the
duration, the model, the log under `runs/jN/`), the log carrying the
drain's summary beside the last fire's stdout and stderr, the loud
settle errors on stderr. A once review job removes its crontab line
after the fire, like every once job.

Rejected, named: the kind applies to prompt jobs only — a command job
runs `sh -c <command>`, and a command literally spelled `review` is a
shell script the operator wrote, not the chore. A headless worker and
an ordinary job never review their own rows; the chore drains the
store, it does not change what a fire is.
