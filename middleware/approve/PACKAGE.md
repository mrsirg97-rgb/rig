# middleware/approve

## What it is

The manual tool-approval gate (SPEC_MODES 4): a `ToolMiddleware` wired
at the root with three closures; the dial, the ask door, and the
mutating set, so the frozen core and the Frontend seam are untouched.
In manual mode a mutating call pauses for the operator's answer; a
denial is a teaching refusal the model reads, never a dead turn.

## What it includes

- `Auto`, `Manual`, `Mode(s)`: the dial's vocabulary; empty descends
  to auto; anything else is the caller's refusal to name.
- `Gate(mode, ask, mutating)`: the gate. `mode` is read at call time (a
  flip applies to the very next call); `ask` blocks for the operator's
  answer (false declines); `mutating` names the calls that pause; the
  read set passes silently, or manual is death by a thousand confirms.
- `Prompt(call)`: the ask row's text: the tool's name and a one-line
  preview of its arguments, truncated to 120; a `delegate` call shows
  the task's first line instead of the wire (SPEC_DELEGATE 7).
  `PromptGeneric` is the plain shape.

## Gotchas

- The gate sits inside the allow-list and the provenance rule (it runs
  after both), so the operator is only ever asked about a call that
  would actually run.
- A nil ask door never reaches the gate in production: the root wires
  the gate only when the frontend can ask, but the gate still refuses
  safely, declining with the named reason rather than executing unasked.
- The denial's text is model-visible and final for that call: "adjust,
  or ask what they want"; the turn continues.
- Under a concurrent batch (SPEC_EVT 6) only barriers reach the gate, so
  asks stay one at a time while reads run.
