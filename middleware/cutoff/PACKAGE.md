# middleware/cutoff

## What it is

The refuse-before-execute link for a tool call the provider marked as
cut off (SPEC_HARDENING 10): a `ToolMiddleware` that reads `ToolCall.Cut`
and refuses a marked call before it reaches the tool, in a teaching
voice. A cut call is data, not intent; the half of a command never runs.

## What it includes

- `Middleware()`: the link. A call with `Cut` empty passes through; a
  marked call returns the refusal without touching the tool.

## How it is consumed

- The provider sets `Cut` to the stream's finish reason when a call's
  arguments are invalid at stream end (the openai adapter marks a
  length-cut or malformed call; `Done` follows, never `Fault`).
- The loop's ordinary tool-result path feeds the refusal back: the
  transcript keeps the reasoning, the partial call, and the refusal, and
  the model re-issues on the next turn. No dead turn, no `Fault` row.
- Wired after the allowlist and before the approve gate, so a doomed
  call is never offered to the operator for approval.

## Gotchas

- `Cut == "length"` reads as the budget voice ("re-issue more tersely,
  or split"); any other finish reason reads as the malformed-call voice
  ("not valid JSON; re-issue"). A provider that marks a call with a
  reason the link has not seen gets the malformed voice, which is safe.
- The refusal is a failure, so the guard's bound still counts it: an
  identical re-issuance is bounded, and `Rounds` bounds the turn.
- The marker does not ride the store: the partial args and the refusal
  are what the transcript keeps, and a resumed session never re-executes
  a call that already has a result.
