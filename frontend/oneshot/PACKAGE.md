# frontend/oneshot

## What it is

The single-prompt Frontend: the first `Input` yields the prompt, the
next ends the session (`io.EOF` is the loop's clean exit). The
scheduler's worker path and `delegate` use it: argv supplies the
prompt, the process's stdout is the response.

## What it includes

- `New(prompt, out)`: refuses an empty prompt at construction
  (`ErrOneShot`): a one-shot with no prompt is a construction error, not
  an empty turn (the loop's blank-line skip would otherwise swallow it).
- `Input`: the prompt once, then EOF.
- `Notify`: assistant text straight through, faults loud. Tool events
  stay out of the worker's stdout: their results are the turn's
  substance, not its report. `Err` carries the worker's liveness: the
  reasoning deltas as they stream, one line at tool start and end, and a
  periodic heartbeat while a tool runs — the scheduler's stall watch
  reads bytes, and a worker silent during a long tool run must never
  look hung. `Heartbeat` (default 30s) sets the cadence.
- `Faulted`: whether any fault crossed the session. The run-job record
  derives status from the exit code, so a faulted worker must exit
  non-zero or the run logs as ok.

## Gotchas

- The frontend never asks: an approval gate is not wired for one-shot
  runs (SPEC_MODES 4).
