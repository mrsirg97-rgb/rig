# policy/empty

## What it is

The empty-turn guard (SPEC_EMPTY): a provider decorator at the same seam as
the overflow retry (`compact.Decorator`). A stream that finishes normally
with no content and no tool calls is discarded and resampled with the
identical request, at most twice, then a fault surfaces. Stdlib only.

## What it includes

- `Decorator(inner)`: wraps a `core.Provider`. `Stream` calls the inner
  provider and relays its events; on an empty turn it withholds the
  attempt's `Done` and deltas, emits `core.EmptyTurn` (the notice plus the
  discarded usage), and resamples by calling `inner.Stream` with the same
  request. The relay is one goroutine, so the retry is invisible to the
  loop.
- `faultMessage`: the surfaced fault's plain words, with the "tool call
  written inside its thinking" clause only when the last reasoning carries
  a tool-call marker (`<invoke name=`, `<tool_call>`, `<function=`).
- `toolCallMarkers`, `maxResamples` (2): the clause set and the budget.

## How it is consumed

- The root wires it inside `compact.Decorator`, outside `effort`/`openai`:
  the resample bypasses compact's re-clamp so the request stays
  byte-identical, and compact's calibration sees only the kept turn's
  `Done`.
- The recorder and frontends see `core.EmptyTurn` through the loop's
  existing default: the recorder discards its partial and adds the usage;
  the CLI/TUI render the one-line notice and count the usage.
- A cancel at any point (an attempt or a resample) closes the relay clean;
  the loop reads the normal interrupt path.

## Gotchas

- Deltas stream live: the empty attempt's reasoning has been shown by the
  time the turn is known empty, and the discard is marked by the notice,
  not hidden. The recorder drops its partial on `EmptyTurn`, so the store
  never writes the discarded reasoning. The one residue is the loop's own
  accumulation: it appends one message per stream, so the in-memory
  session message carries the shown reasoning beside the kept turn's; the
  store and the resample's request stay clean (SPEC_EMPTY 2).
- Only `finish_reason "stop"` with trimmed-empty content and zero calls is
  an empty turn. A `length` cut, a fault, or a truncated stream pass
  through untouched: every delta is forwarded as it arrives, and a
  non-`stop` `Done` or a `Fault` goes through with the partials as shown.
- The resample reuses the request exactly: no re-clamp, no reassembly, no
  nudge. The wire request is byte-identical, so the server's prompt cache
  is a full hit.
- A `Fault` ends the relay: the guard does not retry a faulted stream, and
  the overflow retry (`compact.Decorator`) composes with it as before.
