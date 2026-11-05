# rig: the piped frontend's reasoning/answer boundary

The piped CLI renders `ReasoningDelta` and `TextDelta` verbatim, so a turn
that thinks and then answers prints `thinking answer` with no boundary
between the two. The operator reads the stream live and cannot tell where
the thinking stopped and the answer started. The TUI already separates the
two slots (SPEC_TUI decision 10); the piped reference does not.

## goals

- One newline, written by the CLI, at the moment a turn's text switches
  from reasoning to answer. The reasoning and the answer stay verbatim
  otherwise: no styling, no trimming, no second newline.
- The boundary is a property of the stream, not of the model: the CLI
  tracks the last text kind it rendered and writes the newline on the
  reasoning-to-answer transition, once per switch, however many
  `TextDelta`s carry the answer.
- A turn with no reasoning gets no extra newline: the answer is the
  answer, byte for byte.

## non-goals

- No change to the TUI, the web chat, or the one-shot frontend: the TUI
  styles the two slots already, the web chat renders them as panes, and
  the one-shot worker's stdout is the answer alone.
- No change to the wire, the loop, or the recorder: the separator is a
  render decision of the piped frontend, and the transcript keeps the
  deltas as they arrived.
- No styling, no labels, no blank line: one `\n`, nothing else.

## decisions

### 1. The state is the last rendered text kind

`cli` carries one fact: whether the last text it rendered was reasoning.
`ReasoningDelta` writes its text and sets it; `TextDelta` writes one
newline first when it is set, then its text, and clears it. A tool call
clears it too: the execution bracket (`● name`, the result line) already
separates the reasoning from the answer that follows the tool, so the
transition across a tool is not a reasoning-to-answer switch.

`TurnEnd` clears it: the boundary is the turn's, and the next turn starts
clean. `EmptyTurn` does not: the discarded attempt's reasoning was shown,
and the resample's answer follows it, so the newline belongs there.

### 2. The newline is written, not inferred

The CLI writes exactly one `\n` on the transition, whatever the reasoning
ended with. A reasoning delta that already ends in a newline gets a second
one; that is the model's text staying verbatim, and the CLI's one newline
is the boundary it promised. The alternative — inspecting the last byte —
makes the render depend on the model's whitespace, which is exactly what
"verbatim otherwise" refuses.

## layout

```
frontend/cli/notify.go   the last-text-kind fact and the one newline
frontend/cli/cli_test.go the renamed transition test and the no-reasoning guard
frontend/cli/PACKAGE.md  Notify's boundary named
specs/SPEC_NOTIFY.md     this file
```

## tests

- Reasoning then answer, the answer in several deltas: the reasoning is
  verbatim, exactly one newline separates it from the answer, and the
  answer is verbatim.
- A turn with no reasoning: the answer gets no extra newline.

## scope

What this is not: a change to any other frontend, to the wire, or to the
transcript. The separator is the piped reference's, and it is one byte.
