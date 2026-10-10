# provider/anthropic

## What it is

Anthropic's Messages API as a `core.Provider`: the second implementer
of the one-method seam, beside `provider/openai`. Plain JSON and SSE
over net/http, stdlib only, no SDK. The loop sees `core.Event` only;
the wire dialect is this package's problem.

## What it includes

- `New(Config) core.Provider`: the one constructor. `BaseURL` (default
  `https://api.anthropic.com`; the request posts `{BaseURL}/v1/messages`
  with `stream: true`), `Model`, `APIKey` (the `x-api-key`
  header; never in a fault, a notice, a log or a wire fixture),
  `Version` (the `anthropic-version` header; default `2023-06-01`),
  `MaxTokens` (the fallback when a request carries none — the API
  requires the field, and a request without one and a Config without
  one is a loud `Stream` error), `Thinking{Budget}` (the row's
  thinking budget; 0 = off, and off sends no thinking blocks in either
  direction), `CacheControl` (the three breakpoints, below), `Retries`
  (429 and 5xx), `RetryBase`/`Jitter` (the backoff's test seams),
  `HeaderTimeout` (zero is the 5-minute default, `HeaderTimeoutOff`
  removes the bound), `IdleTimeout` (zero is the 10-minute default),
  `BlobsDir` (vision), and the four per-million prices `InputPrice`,
  `OutputPrice`, `CacheReadPrice`, `CacheWritePrice` (the cost column:
  zero when absent, no price table in Go). `Thinking{Budget}` is the
  row's thinking budget; 0 = off, and off sends no thinking blocks in
  either direction. A request whose `max_tokens` leaves no room under
  the budget — the compact clamp lowers it, the summary call sets it —
  drops thinking for that request, thinking blocks included.
- The wire (`wireRequest`, `wireMessage`, `wireContent`, `wireEvent`,
  …): the system prompt rides the top-level `system` array of text
  blocks; `Tools` ride `tools[]` with `input_schema` from
  `ToolSpec.Schema`; the transcript rides `messages[]` of content
  blocks — user text, assistant `tool_use` (the `core.ToolCall` id
  verbatim, so results pair), user `tool_result` batched into one user
  message per run of results, `thinking`/`redacted_thinking` blocks
  carried back verbatim when thinking is enabled, and view results as
  `image` blocks with a base64 source inside their `tool_result`. The
  assistant replay orders thinking, then the text, then the tool
  calls — the generation order the API replayed — and skips an
  assistant turn that encodes to no blocks rather than send
  `content: null`. An empty tool result rides a named `[no output]`
  text block (the API rejects an empty one). Only the anthropic
  records ride: another provider's `reasoning.text`-shaped details and
  an empty thinking record are dropped.
- The cache breakpoints (`CacheControl`): `cache_control:
  {"type":"ephemeral"}` on the last system block, the last tool, and
  the last user block of the prior turn — three of the four the API
  allows. The system and tool tables are byte-stable per session, so
  their entries write once and read every turn; the transcript
  breakpoint sits on the prior turn's tail so the writes stay one per
  user turn (within a tool round-trip the prefix through the
  breakpoint is unchanged and re-read, not rewritten). The body is
  pinned byte-for-byte by a test: the prefix cache is byte-keyed, so a
  rewrite, a reorder, or a nondeterministic field in this encoding is
  a cache regression.
- The stream (`Stream`): SSE in, `core.Event` out. `message_start`
  seeds the usage's input and cache fields and echoes `model`; the
  final `message_delta` may repeat the cache fields — compat runtimes
  report them only there — and `Usage.Prompt` is summed at `Done`
  (`input_tokens` + `cache_read_input_tokens` +
  `cache_creation_input_tokens` — the anthropic `input_tokens`
  excludes the cached tokens, and the sum is what openai's
  `prompt_tokens` already means).
  `text_delta` → `TextDelta`; `thinking_delta` → `ReasoningDelta`
  live, with the block record (`{"type":"thinking","thinking",…
  "signature"}` or `{"type":"redacted_thinking","data"}`) emitted on
  its `signature_delta`, or at the block stop when none came — the
  record rides `ReasoningDelta.Details` into
  `Message.ReasoningDetails`, the carrier the carry-back decodes.
  `input_json_delta` accumulates per block into ONE `ToolCallEvent`
  at the block's stop. `message_delta` carries the stop reason and the
  output tokens; `message_stop` → `Done` once.

## How it is consumed

- `cmd/rig`'s `buildProvider` selects it when the row's `provider` is
  `anthropic`; the row supplies the base URL, the key, the budget, the
  prices and the retries (SPEC_HOSTED decision 7). Nothing else names
  the package.
- The stop reasons map into the vocabulary the loop already speaks:
  `end_turn`/`stop_sequence` → `stop` (the empty decorator's resample
  key), `tool_use` → `tool_calls`, `max_tokens` → `length`. `refusal`
  is a `Fault`, not a `Done`.

## Gotchas

- An empty message list is a loud `Stream` error, and so is a request
  and a Config that together carry no `max_tokens`.
- A thinking budget needs room to answer: `models.Check` refuses a
  budget at or past the row's `maxTokens`, and one under the API's own
  1,024 floor. A request whose `max_tokens` is lowered under the
  budget sends no thinking at all.
- A tool call whose args are not valid JSON when the stream ends keeps
  its raw partial args and carries `Cut` set to the mapped stop reason
  (the cutoff link refuses it before the tool, SPEC_HARDENING 10); on
  the next request its `input` rides as `{}` — the API requires a JSON
  object there, and the cutoff refusal already told the model to
  re-issue. A tool block the server never closes is flushed at stream
  end under the same rule.
- A stream that ends without `message_stop` is
  `anthropic: stream truncated: no finish marker`. An `error` event or
  a non-2xx body is a fault with the API's `error.type` and message.
- 429 and 5xx re-post the identical body inside the stream goroutine
  up to `Retries`, honouring `retry-after` (seconds; a decimal parses)
  over the backoff `base * 2^attempt * (1 + jitter)`. The retry lives
  at the status gate: once a byte has streamed a failure is never
  retried.
- The SSE `event:` name lines, comments and blank lines are skipped —
  the real API sends `event:` lines beside every `data:`; the data
  line is the only payload carrier, so a folded payload (multiple
  `data:` lines for one event) would fault as malformed.
- The scanner buffer is bounded (64 KiB initial, 4 MiB max); the
  error body is read capped at 256 bytes.
- The blob loader (`images.go`) is duplicated from
  `provider/openai`'s on purpose: the same sha-verify and note voices,
  because a lift would edit a package this change need not touch.
  Collapse them when a third provider needs it. The inline bound is
  this API's: 5 MiB measured on the base64 data, which is what the API
  checks (openai bounds the decoded bytes) — an image that encodes
  over it rides the note instead of the wire.
- The live smoke (`smoke_test.go`) runs behind `RIG_SMOKE_ANTHROPIC_MODEL`
  plus either `RIG_SMOKE_ANTHROPIC_KEY` (a real key against the
  api default base) or `RIG_SMOKE_ANTHROPIC_BASE_URL` (keyless, a
  Messages-format server). It proves the wire and the stream against a
  live server, not the semantics that server lacks — signatures,
  cache reads, refusals, 429s and the error bodies stay pinned by the
  fake-server tests.
