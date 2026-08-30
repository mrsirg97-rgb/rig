# policy

## What it is

Prompt-assembly implementations. v1 ships the passthrough: system prompt
plus transcript, verbatim. `policy/compact` is the first non-passthrough
`ContextPolicy` (SPEC_COMPACT): below the trigger it is the passthrough,
byte-identical; at the trigger it rewrites the older transcript into a
summary through the same `core.Provider` and returns system + summary +
kept tail. It also carries the overflow decorator (7): a provider fault
that names context length triggers one compact-and-retry, once, then
surfaces. Stdlib only; the summary prompt is one embedded file.

## What it includes

- `Passthrough(system)`: the v1 policy: system (when set) + transcript,
  verbatim, pure across repeated assemblies.
- `compact.New`: the compact policy.
- `compact.Compact`: the forced seam (SPEC_COMMANDS 3).
- `compact.Estimate`: the raw stdlib estimate (decision 4).
- `compact.sizeOf`, `clampMaxTokens`: the anchor-aware size and the
  max-tokens clamp.
- `compact.split`, `callIn`: the keep-recent cut.
- `compact.RenderTranscript`, `SummaryInput`, `summarize`: the summary
  call's shape and execution.
- `compact.Decorator`, `classifiesContextLength`: the overflow recovery.
- `compact.recoveryOwed`, `spendBudget`, `calibrate`: the once budget and
  the delta-only factor update.
- `SummaryMarker`, `SummarySystem`: the marker and the summary role.
- `effort.Decorator`: the effort dial's provider decorator (SPEC_MODES 1):
  stamps the session's effort onto a request that has none; the compaction
  summary call's own (the row's) is untouched.

## How it is consumed

- `Passthrough` and `compact.New` implement `core.ContextPolicy`: the root
  wires one into the kernel.
- `compact.New` takes the provider, the frontend (the recorder), the
  session, the assembled system string, a checked `models.Model`. A nil
  seam or a violating row refuses loud at construction.
- `compact.Decorator` wraps the same inner provider the policy uses: the
  summary call does not pass through it (1), so a fault in it surfaces as
  an `Assemble` error, never recursively.
- `compact.Compact` returns `(core.Compacted, bool, error)`: the caller
  owns delivery of the event (the /compact verb), exactly once.

## Gotchas

- `SummaryMarker` (`[compaction] `) makes summary rows self-describing:
  they are exactly the user rows that start with it; grep is the
  interface.
- `compact` is impure by design: one rewrite, one provider call, one
  event, and not idempotent; a second compaction is a fold (3). The
  reflection is cut (SPEC_COMPACT 6): the summary is context, not memory.
- Emission is exactly once per compaction: `Assemble` delivers the event
  to the frontend it holds (the trigger path); the overflow path delivers
  it on the stream instead (decision 5). The `Compacting` cue (the
  loader's cue, SPEC_COMPACT 5 amended) goes to the frontend directly on
  both doors, before the summary call.
- The once budget is structural (the transcript length at the last
  attempt), not a retry limit; a second context-length fault against the
  same transcript surfaces; never a silent loop.
- `sizeOf` anchors on the last `ContextTokens > 0` message: no anchored
  message means the whole list is estimated. `calibrate` is delta-only:
  `reported - anchor` isolates the delta; a delta under 2% of the anchor
  is not a measurement and leaves the factor alone; the factor is clamped
  to `[0.5, 2.0]` and stays 1.0 until the first trusted report
  (SPEC_COMPACT 4, amended 2026-08-21: a tiny tool-loop delta pinned the
  factor at 4 and the brain compacted at ~50k).
- `Estimate` counts `Reasoning` on the last assistant message only: the
  chat templates strip it from history, so the server never counts it. A
  single-message list (the `split` budget) counts its own.
- An older prefix whose summary input does not leave the summary floor
  (`min(Reserve/4, 256)`) is cut to the oldest slice that does
  (`fitPrefix`: a binary search over the rendered input, then back to a
  call boundary); the remainder rides ahead of the tail and folds on a
  later pass. A prefix of one message that does not fit is the loud
  failure (SPEC_COMPACT 3, amended 2026-08-21).
- `clampMaxTokens` refuses loud when the kept batch overruns the window
  (budget below the smaller of `Reserve/4` and 256); surfaced as a Fault
  so `-p` exits non-zero.
- `split` is the keep-recent cut: never empty, the tail's first message
  is never a `RoleTool` result whose call is in the transcript, a
  multi-call batch is atomic, and a dangling result (the resume shape)
  may start the tail. The budget uses the tail's own calibrated estimate,
  not the anchor.
- `summarize` uses the row's `Effort` (the one call whose thinking nobody
  reads), `"medium"` the default; the stream must end with `Done` and
  non-empty text, else loud.
- `New` baselines the once budget at the transcript length at
  construction; the root builds the session before the policy, so a
  resumed session starts at that baseline.
