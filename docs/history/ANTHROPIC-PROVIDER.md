# the second provider: anthropic's Messages API

**History, kept.** The design note for 2.15.0, written before the code
and preserved as written — including the facts it verified against
Anthropic's docs on the day (the version header, the usage semantics,
the caching economics). Current truth is `provider/anthropic/
PACKAGE.md` and SPEC_HOSTED decision 7.

## the claim it tests

`core.Provider` is one method, `Stream(ctx, req) (<-chan core.Event,
error)`, and it has had one implementer since 0.x. Claude is reachable
today through OpenRouter, so the gain is not access; it is native
semantics: explicit `cache_control` breakpoints where the words-are-
the-budget work says the bulk of every turn sits (the system prompt
and the tool table), native thinking blocks with a token budget
instead of a `reasoning_details` translation, Anthropic's own stop
reasons and usage (`cache_read_input_tokens`,
`cache_creation_input_tokens`) feeding the cost column. And it is the
first test of the seam's claim: a new behavior is a new implementer at
the root, never a branch in the caller. The loop, the policy and
`core/` take zero diff; the structural test is that the diff touches
`provider/anthropic`, `models`, `config`, `cmd/rig/root.go` and docs
only.

## 1. the shape

`provider/anthropic`: `New(Config) core.Provider` in the shape of
`provider/openai` — the struct unexported, nothing outside the package
names it, stdlib only (net/http + SSE, no SDK).

```go
type Config struct {
    BaseURL       string        // default https://api.anthropic.com
    Model         string
    APIKey        string        // x-api-key header; never in a fault, a log, a test fixture
    Version       string        // anthropic-version; default the dated value, 2023-06-01
    MaxTokens     int           // the fallback when a request carries none
    Thinking      struct{ Budget int } // the row's thinkingBudget; 0 = off
    CacheControl  bool          // the three breakpoints below
    Retries       int           // 429 and 5xx
    HeaderTimeout time.Duration // zero = 5m default, HeaderTimeoutOff = off
    IdleTimeout   time.Duration // zero = 10m default
    BlobsDir      string        // vision: the image blob store (imagemarker)
    InputPrice, OutputPrice, CacheReadPrice, CacheWritePrice float64 // per-million, from the row
}
```

- `POST {BaseURL}/v1/messages` with `stream: true`; headers
  `x-api-key` and `anthropic-version` plus the JSON content type. The
  key rides the header and nothing else: the wire test pins the header
  with a fake key, and no fault text, notice or log line can carry it.
- `max_tokens` is required by the API: the request's value wins
  (the compact decorator stamps it on every main call), else the
  Config's (the row's), else a loud Stream error. No default number is
  invented.
- No `temperature` and no `top_p`: with thinking enabled they are
  constrained anyway, and the row carries no dial for them. No unix
  socket form: the local box serves the wire over http (llama-swap);
  `unix:` stays an openai capability. No env vars: the RIG_* surface
  is unchanged (SPEC_CONFIG 8); the new row fields are file fields.
- `Version` defaults to `2023-06-01`, verified against Anthropic's
  docs as the current dated value of the header; a Config value
  overrides it, nothing else sends it.

## 2. the wire

The body: `model`, `max_tokens`, `stream: true`, and:

- **system**: the `RoleSystem` messages leave the transcript and ride
  the top-level `system` array, one text block each, in order — the
  API's own shape for the system prompt, and the first breakpoint's
  host.
- **tools**: `tools[]` as `{"name", "description", "input_schema"}`
  with `input_schema` from `ToolSpec.Schema` (raw JSON, the object
  shape openai's wire test already pins).
- **messages**: the transcript as content blocks.
  - user text: `{"role":"user","content":[{"type":"text",...}]}`.
  - assistant: thinking records first (the API requires thinking
    blocks at the front of the assistant message for tool-use
    continuity), then `tool_use` blocks `{"type":"tool_use","id",
    "name","input":<args>}` in call order, then the text block. Every
    `core.ToolCall.ID` rides verbatim as the `tool_use` id, so the
    results pair by id.
  - tool results: consecutive `RoleTool` messages batch into ONE user
    message of `tool_result` blocks `{"type":"tool_result",
    "tool_use_id","content":[...]}` — the API's parallel-call shape,
    and it keeps the image inside the result it belongs to.
  - images: a view result's marker is parsed (imagemarker) and the
    blob rides a `{"type":"image","source":{"type":"base64",
    "media_type","data"}}` block inside its `tool_result` content —
    no splice into a synthetic user message, which the openai wire
    needs because its image part has no tool_result home.
  - **the thinking carry-back**: a thinking block streams as
    `thinking_delta` text (live `ReasoningDelta`) plus one
    `signature_delta`; at the block's stop the provider emits one
    `ReasoningDelta{Details:[record]}` whose record is the block
    verbatim — `{"type":"thinking","thinking":<text>,"signature":
    <sig>}` or `{"type":"redacted_thinking","data":...}`. The loop
    accumulates `Details` into `Message.ReasoningDetails` (the same
    carrier the openai provider uses for reasoning_details), so the
    carry-back is: when thinking is enabled, decode each record and
    emit the block verbatim; when disabled, send no thinking blocks.
    An empty signature (the local Maya runtime's) is carried as it
    arrived; the fake tests pin real signatures, the smoke test
    proves the wire against the server that ignores them.
- **cache_control** (`CacheControl: true`): `{"type":"ephemeral"}` on
  exactly three blocks — the last system block, the last tool, and
  the last user block of the prior turn. The why, from Anthropic's
  caching docs: a cache write happens only at a breakpoint, and a
  read looks backward from a breakpoint for a prior entry. The system
  and tool tables are byte-stable for the whole session, so their
  breakpoints write once and read every turn. The transcript
  breakpoint sits on the prior turn's last user block because that
  makes the writes one per user turn: within a tool round-trip the
  prefix through the breakpoint is unchanged (the assistant's
  tool_use and the tool_results append after it), so each round-trip
  request re-reads the same entry instead of rewriting it, and the
  next turn's breakpoint finds the previous turn's entry one turn
  back, inside the 20-block lookback. The fourth breakpoint stays
  unused: three named positions, no fourth rule to maintain.
- The body is pinned byte-for-byte by a test (system + tools + a
  transcript with the three breakpoints), because the prefix cache is
  byte-keyed: a rewrite, a reorder, or a nondeterministic field in
  this encoding is a cache regression, not a cosmetic one.

## 3. the stream

SSE in, `core.Event` out:

- `message_start`: `message.model` is the echo (`Done.Model`);
  `usage.input_tokens` (+ `cache_read_input_tokens` +
  `cache_creation_input_tokens`) seeds the prompt usage. Verified
  against the docs: `input_tokens` excludes the cached tokens, so
  `Usage.Prompt = input + read + write` — the sum openai's
  `prompt_tokens` already means, and the loop's ContextTokens stamp
  stays comparable across providers.
- `content_block_start/delta/stop`: `text_delta` → `TextDelta` live;
  `thinking_delta` → `ReasoningDelta` live; `signature_delta` kept
  for the record; `input_json_delta` accumulates per index into ONE
  `ToolCallEvent` at the block's stop (id, name, and the args as raw
  JSON — the id pairs the result). `redacted_thinking` and `ping`
  need no delta handling.
- `message_delta`: the final one carries `stop_reason` and the
  cumulative `usage.output_tokens`.
- `message_stop` → `Done{StopReason, Usage, Model}` once. The stop
  reason maps into the vocabulary the loop already speaks, so the
  paths downstream fire identically: `end_turn` and `stop_sequence` →
  `stop` (the empty decorator's resample key), `tool_use` →
  `tool_calls`, `max_tokens` → `length` (the truncation path: a
  tool call whose args never completed carries `Cut` exactly as
  openai marks one, and the cutoff link refuses it before the tool).
  `refusal` is not a Done at all: a `Fault` naming it.
- A stream that ends without `message_stop` is
  `anthropic: stream truncated: no finish marker` — the same words as
  openai's.
- An `error` event mid-stream, or a non-2xx status, is a fault with
  the API's `error.type` and message (`anthropic: <status>:
  <type>: <message>`; the bare body when it is not the envelope).
  The key cannot appear: it went out in a header.
- 429 and 5xx retry per `Retries`, honouring `retry-after` (seconds)
  when the header is present, backoff `base * 2^attempt * (1 +
  jitter)` when it is not; the retry re-posts the identical body
  inside the stream goroutine and never runs after a streamed byte.
- The idle bound (10m default, reset per line, the connection closed
  at the bound) and the header timeout (5m default,
  `ResponseHeaderTimeout`) as SPEC_HARDENING 11 and
  `provider/openai` apply them.

## 4. the row and the root

`models.Model` gains `ThinkingBudget int` (`thinkingBudget`) and four
optional prices `InputPrice`, `OutputPrice`, `CacheReadPrice`,
`CacheWritePrice` (`inputPrice`, …; per-million dollars, zero when
absent). `Check`:

- `thinkingBudget` and `cacheControl` are admitted for
  `provider: "anthropic"`; both are refused elsewhere by name
  (thinkingBudget is anthropic-only; cacheControl stays
  openrouter-or-anthropic).
- `providerPin` and `reasoning` are refused on an anthropic row by
  name: the pin is OpenRouter's upstream order and `reasoning` names
  an openai-compatible wire field; anthropic's field is the block
  stream itself.
- the prices are admitted for anthropic alone (openrouter reports
  `usage.cost` itself; a row price there would be a second truth).

`config/modelsfile.go` admits the keys; `buildProvider` gains the one
branch the seam promises — `Provider == "anthropic"` constructs
`anthropic.New` with the row's fields, everything else stays
`openai.NewWithConfig` unchanged. The cost column is the row's prices
times the usage the stream reports — no price table in Go:

    cost = inputPrice·(input)/1e6 + cacheWritePrice·write/1e6
         + cacheReadPrice·read/1e6 + outputPrice·output/1e6

(zero when the row names no prices), computed in the provider at the
`Done` and riding `Usage.Cost` into the store's cost column the way
openrouter's wire cost does. Nothing in `loop/`, `core/` or the
policy changes.

## 5. tests

Against a fake Messages server, the way `provider/openai`'s tests
drive a fake: the body pinned byte-for-byte including the three
breakpoints; a tool-use round trip (tool_use → ToolCallEvent →
tool_result pairing by id); thinking blocks carried back with
signatures (and redacted_thinking verbatim); every stop reason; each
error path's words; the truncated stream's fault; 429 with
retry-after; the usage and cost arithmetic from the row's prices; a
5-minute header timeout on a silent server; the idle bound;
cancellation. One live smoke test behind env vars (`RIG_SMOKE_API_KEY`
with a real key, skipped by default; `RIG_SMOKE_BASE_URL`, keyless,
against the operator's llama-swap serving the Messages format at
`http://localhost:8090/v1/messages`): it proves the wire and the
stream against a live server, not the semantics that server lacks —
its thinking `signature` is an empty string, it ignores
`cache_control`, reports no `cache_creation_input_tokens`, and never
sends a refusal, a 429 or Anthropic's error bodies, so those stay
pinned by the fake-server tests.

## named not done

- No temperature dial, no `top_p`, no stop sequences, no tool_choice
  pinning: the request carries what `core.Request` carries.
- `Request.ReasoningEffort` is not mapped onto a thinking budget; the
  budget is the row's dial. A per-effort budget table is a later
  decision if the operator wants one.
- The image blob loader is duplicated, not lifted into a common
  package: a lift edits `provider/openai` for no behavior change, and
  the duplication rule says lift only what lifts without a flag.
  Collapse it on a consolidation pass if a third provider ever needs
  it.
- No shared SSE machinery: the scanner, retry and timeout shapes are
  each ~40 lines and differ in the details that matter (retry-after,
  block accumulation); a common chassis would grow flags.
