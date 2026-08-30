# frontend/cli

## What it is

The stdin/stdout Frontend and the piped reference (SPEC_COMMANDS,
SPEC_TUI decision 10): `Input` is a blocking pull of one user message;
`Notify` is a fire-and-forget observation of the turn stream, rendered
as greppable plain text; deltas verbatim, the execution bracket as
status lines, faults as marked lines, one usage line at the explicit
turn boundary. It dispatches the user commands and owns the steering
seam (SPEC_HARDENING decision 4, SPEC_COMMANDS 2).

## What it includes

- **Input**: the blocking pull: the steering slot delivered before
  blocking, the command dispatch consumed inside Input, blank lines as
  no-ops, EOF ending the REPL, a cancelled context surfacing its error.
- **Notify**: the render of the named events: `TextDelta`/`ReasoningDelta`
  verbatim, `ToolStart`/`ToolResult` as the `● name` bracket, `Done`,
  `Compacting`/`Compacted` (the loader line and the one-line compact
  event), `Fault` as `[fault]`, and `TurnEnd`'s usage line. Events it
  does not name are ignored (the compat rule).
- **The reader goroutine**: owns stdin: a line that lands while Input
  is blocked delivers direct (between turns); a line during a live turn
  goes to the slot and interrupts the turn, marking the "an interrupt
  just landed" fact.
- **The Steerer** (SPEC_COMMANDS 2): `Steer`, `Interrupt`, `ClearSlot`,
  `LiveTurn`; the slot (latest wins), the interrupt handle, and the
  liveness fact, the frontend's contract, not the root's.
- **The dispatch**: the prefix rule (`IsCommandLine`), the registry,
  the sorted known names for the refusal voice, and exactly one printed
  line per command (the error, or the reply if non-empty).

## How it is consumed

- The root wires it with `cli.New(in, out, cli.WithCommands(command.All(), env))`.
  `WithCommands` fills the frontend-owned `Steer` seam in the env (the
  dispatcher's contract).
- `Input` returns one user message (the loop's contract): the loop never
  sees a command; `//` is the escape (one slash consumed), an unknown
  command is a loud line naming the known set, never silently a prompt.
- `Notify` is fire-and-forget from the loop's turn stream: the CLI is
  the piped reference and adds nothing the runtime does not emit.
- `LiveTurn` is structurally false at dispatch (the loop is in
  awaiting_input); the compact / new / sessions resume refusals read it.

## Gotchas

- One slot, latest wins: not a mailbox. Sequential delivery means no
  locking beyond the channels. A paste burst or the window between
  turns delivers lines in order to the next Input, not to the slot
  (latest-wins would silently drop all but the last line).
- The turn's usage totals accumulate across the turn's model calls and
  print at `TurnEnd` (`hit = CacheRead / Prompt`, the wire's cached
  subset), then reset.
- A delivered slot line consumes the "interrupt landed" fact: it is
  the line that broke the turn and became a prompt. A line typed at a
  quiet prompt is a clean boundary: the fact does not survive it.
- The dispatcher owns the line's frame: the command's output is the
  bytes, and exactly one newline ends the line; a multi-line listing
  carries its own trailing one, and the frame is not a second.
- The thinking is visible verbatim (the CLI does not style it): the TUI
  styles it (SPEC_TUI decision 10).
