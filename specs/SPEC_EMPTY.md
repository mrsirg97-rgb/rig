# rig: the empty-turn guard

A session store record showed the failure: messages.seq 13171, model
huihui3.8-flash, the assistant turn had content "", zero tool calls, 268
completion tokens, finish_reason "stop". Its reasoning ended with a fully
formed tool call (`<invoke name="edit"> ... </invoke>`) that the model wrote
inside its thinking block without closing it. The server filed it as
reasoning, so rig got an assistant turn with nothing to say and nothing to
run, and the turn ended. The operator had to type "you good?" to restart.
Server side was clean: HTTP 200, truncated = 0, nowhere near maxTokens.

## definition

An empty turn: the stream finished normally (`finish_reason "stop"`), the
assistant content is empty or whitespace, and there are zero tool calls.
Reasoning may be non-empty; the whole point is that a model that thinks a
tool call but never says it left a turn with nothing to execute. A turn cut
by `length` is NOT an empty turn; it already has its own fault.

## decisions

### 1. The guard is a provider decorator that resamples the same request

`policy/empty` carries `empty.Decorator`, the same shape as the overflow
retry (`compact.Decorator`, SPEC_COMPACT 7): it wraps the inner provider,
relays its events, and on the condition resamples by calling `inner.Stream`
again, inside its own relay goroutine. `loop/loop.go` is untouched.

On an empty turn the decorator discards it and resamples the SAME
`core.Request`: same messages, same tools, same MaxTokens, same effort. The
wire request is byte-identical to the first attempt, so the server's prompt
cache is a full hit and the history stays clean. No nudge message is
appended, and the empty turn is never recorded in the transcript.

At most 2 resamples (3 attempts total). If the turn is still empty, the
decorator surfaces a `Fault`:

```
model returned an empty turn 3 times (no content, no tool call); last reasoning ended with what looks like a tool call written inside its thinking
```

The clause after the semicolon appears only when the last attempt's
reasoning actually contains a tool-call marker (`<invoke name=`,
`<tool_call>`, `<function=`).

Placement: inside `compact.Decorator`, outside `effort` and `openai`. The
resample then bypasses compact's re-clamp (a factor change after the first
attempt's calibration would otherwise change MaxTokens and break the cache
hit), and compact's calibration sees only the kept turn's `Done`.

### 2. Deltas stream live; the recorder drops the discarded attempt

Reasoning deltas are emitted by the provider as they arrive and are
forwarded to the frontend immediately: the operator reads thinking live, so
a normal turn keeps its stream. By the time the decorator sees `Done` (the
earliest moment the turn is known empty), the attempt's reasoning has
already been shown. That is accepted: the discard is marked, not hidden.

The discarded reasoning must still not be persisted. The `EmptyTurn` event
is the marker: the recorder discards its partial buffer on it, so the store
never writes the empty attempt's reasoning, and the resample's `Done` lands
only its own text and thinking. The empty turn leaves no message row.

The one residue is the loop's own accumulation, and it is named: with
`loop/loop.go` frozen, the loop appends one assistant message per stream
from what it accumulated, so the in-memory session message carries the shown
reasoning of the discarded attempt beside the kept turn's (the next request
sends it). The store and the resample's request bytes stay clean; the
recorder is the transcript of record.

### 3. `core.EmptyTurn`: the notice and the usage

```go
type EmptyTurn struct {
    Resample int   // 1-based: this resample
    Limit    int   // total resamples allowed (2)
    Usage    Usage // the discarded attempt's usage, still counted
}
```

The decorator emits it instead of the discarded attempt's `Done` (which is
withheld), then resamples. The loop forwards it in its existing default; the
frontends render one line, `empty turn, resampling (1/2)`; one-shot ignores
it (the worker's stdout is the answer) and still faults on the third empty.

The recorder, on `EmptyTurn`: discards its partial buffer and records the
usage against the last landed message (`AddUsage`: create the row or add to
it, so two discards in one turn merge into one row). The discarded usage is
counted in the session totals; no empty message row is written.

### 4. Everything else passes through untouched

A `Fault` and a stream that ends without `Done` pass through (the loop
keeps its own handling). A `Done` with any finish reason other than `stop`
passes through with its buffered deltas flushed, so a `length` cut keeps the
existing behavior. A ctx cancel during an attempt or during a resample
closes the relay cleanly: the loop reads the same interrupt path as a
cancel during a normal turn, no Fault, no extra call.

## layout

```
core/                 +EmptyTurn (provider-decorator event)
policy/empty/         the decorator: empty.go, PACKAGE.md
store/state/          recorder: EmptyTurn discards the partial and adds usage;
                      AddUsage (upsert-add on the usage table)
frontend/cli/         the one-line notice, the usage added to the turn totals
frontend/tui/         RenderEmptyTurn, the notice committed, the usage added
cmd/rig/              the decorator wired inside compact.Decorator
specs/SPEC_EMPTY.md   this file
```

## tests

Fake provider, no network, loop-level (the decorator + the real loop):

- Empty turn then a good turn: exactly 2 provider calls, the transcript
  holds only the good turn, request bytes of call 2 equal call 1, the
  discarded reasoning streams live and the notice follows it.
- Three empty turns: exactly 3 provider calls, then the fault above;
  transcript unchanged; exactly 2 notices.
- The marker clause: present when the last reasoning carries
  `<invoke name="edit">`, absent otherwise.
- `length` with empty content: not resampled, existing behavior.
- Content, no tool calls: not resampled.
- Cancel during the resample: clean stop, no fault, no extra call.

Recorder level: the discarded usage is counted in the session totals (two
discards merge into one row) and no empty message row is written.
