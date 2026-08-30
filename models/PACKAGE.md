# models

## What it is

The root's per-model table (SPEC_COMPACT 2): window-relative compaction
numbers, one self-contained row per model. Stdlib only, like core; it is
configuration the root owns and reads, not a wire type. The `models`
command reads this same table.

## What it includes

- `Model`: one row: `ID`, `Window`, `MaxTokens`, `Reserve`, `KeepRecent`,
  `Role`, `Effort` (the compaction summary call's request effort; `""` =
  the policy's "medium").
- `Table`: id -> row, built by `New` with every row checked.
- `Check`: the row's invariants, loud, naming the id and fields.
- `overlay` (unexported): applies `RIG_MODEL_*` env onto a row's fields.
- `Resolve`: the root's row resolution at start, before any store opens.
- `Role`: `interactive` / `worker` fleet identity.

## How it is consumed

- `New(rows...)` builds the table from checked rows: a violating row or a
  duplicate id is refused at construction.
- `Resolve(t, id, env)` returns the table row with `RIG_MODEL_*` overlaid
  and re-checked; else, if `RIG_MODEL_WINDOW` is set, a synthesized row for
  the id (absent fields take defaults: `MaxTokens` 8192, `Reserve`
  window/8, `KeepRecent` window/4), validated; else a loud refusal naming
  the id, the known ids, and the env.
- `env` is the lookup seam: `os.LookupEnv` at the root, a map in tests.
- The `models` command reads `Table` and renders it.

## Gotchas

- The row invariants make the pi bug (a global reserve larger than the
  worker's window fires compaction every turn) impossible by construction:
  the reserve is per-row, checked against its own row's window.
- `Reserve` must be in `[0, Window)`: as large as the window, the trigger
  fires at every estimate.
- `KeepRecent` must be in `[0, Window-Reserve)`: the usable window must
  leave room for the summary beside the tail, or compaction can never help.
- Env precedence is env over file over embedded: the overlay makes the env
  win for a known id too (0.2.0 consulted the env only for unknown ids).
- The synthesized row carries `Role: RoleInteractive`: there is no file to
  borrow a role from.
- `overlay` setter map uses closures over the `m` value: parse failure
  returns the raw error and a zero `Model`.
- `Efforts` is the model's available effort levels in its own vocabulary
  and order (SPEC_MODES 1); `Effort` is the row's default, the wire's
  level when the session's dial is unset.
