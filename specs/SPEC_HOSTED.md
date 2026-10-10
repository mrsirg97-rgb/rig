# rig: hosted mode (remote OpenAI-compatible endpoints)

The provider already speaks the OpenAI wire: plain JSON and SSE over
net/http. A hosted endpoint (OpenRouter, DeepSeek's API, any remote
OpenAI-compatible server) speaks the same wire but needs what a local
llama-server does not: an API key, retry against rate limits, cost
accounting, per-provider reasoning field names, and a remote spawn path
that never touches the local swap. This spec adds exactly that: model
rows gain where they run, the provider gains hosted behavior, cost lands
in the usage column, and swarms and scheduled jobs take dollar budgets.
(2.15.0 added a second implementer of the seam that speaks Anthropic's
Messages wire: decision 7.)

## what it is not (named)

- **Not a new provider.** No new wire, no new package, no protocol
  translation. The OpenAI-compatible adapter is the provider; hosted
  mode is configuration on top of it. (Amended by 2.15.0: hosted mode
  gained a second provider — decision 7 — but the amendment rides the
  same row shape and the same one-method seam; nothing here changed.)
- **Not a key vault.** The key is the operator's, in `models.json` or
  an env overlay; it is never logged, never rendered, and never lands
  in an error. There is no key rotation machinery.
- **Not an eviction policy.** A remote endpoint's 429 or 5xx is
  retried with bounded backoff; after the bound it faults loudly like
  any other provider fault. The retry never turns a swarm task death
  into something quieter.

## goals

- One model row carries where it runs: `remote: true` or a provider
  name (`provider: "openrouter"`), plus the row's endpoint (`baseUrl`),
  key (`apiKey`), parallelism bound (`concurrency`), reasoning field
  names (`reasoning`), and OpenRouter extras (`providerPin`,
  `cacheControl`).
- The provider sends `Authorization: Bearer <key>` when the row has
  one, retries 429 and 5xx with exponential backoff and jitter under a
  bound, parses `usage.cost` where the endpoint returns it, reads
  reasoning under the row's field names, echoes it back under the same
  names, and omits llama-server-only fields for remote rows.
- Cost lands in a new `usage.cost` column beside the token counts;
  the session's dollars show in the TUI footer, and swarms and
  scheduled jobs sum it for their budgets.
- A remote spawn (delegate or scheduled fire) never consults the local
  swap: no busy probe, no `WaitBusy`; parallelism is the row's
  `concurrency` token bound plus the worker count.

## decisions

### 1. The row's run site

`models.Model` gains: `Remote bool` (`remote`), `Provider string`
(`provider`; a non-empty name implies remote), `BaseURL string`
(`baseUrl`), `APIKey string` (`apiKey`), `Reasoning string`
(`reasoning`; `reasoning_content` default, `reasoning` for OpenRouter),
`ProviderPin []string` (`providerPin`, a string or array),
`CacheControl bool` (`cacheControl`), `Retries int` (`retries`,
default 3). The row's `concurrency` is a retired key with a startup
notice (2.4.0) — no field on the row, no default, no column.

- `Check` refuses a remote row without `baseUrl` (a remote endpoint
  cannot borrow the swap's URL), a
  `providerPin` or `cacheControl` on a row whose provider is not
  `openrouter`, and a `reasoning` value outside the two names.
- The env overlay gains `RIG_MODEL_BASE_URL`, `RIG_MODEL_API_KEY`,
  `RIG_MODEL_REMOTE` (bool) and `RIG_MODEL_REASONING`, so a key can
  live in the environment and never in a file. The overlay wins over
  the row, like every other field; `RIG_MODEL_API_KEY` in a process
  env is the one place a key may live without being in settings.
  (Amended: `RIG_MODEL_CONCURRENCY` was retired with the row's
  `concurrency` in 2.4.0 (SPEC_WORKERS); the overlay today is
  `RIG_MODEL_WINDOW`/`MAX_TOKENS`/`RESERVE`/`KEEP_RECENT`/`RETRIES`,
  `RIG_MODEL_BASE_URL`/`API_KEY`/`REASONING`/`REMOTE`, and
  `RIG_MODEL_PROVIDER`.)
- `config/modelsfile.go` accepts the new keys and merges them by id
  like the rest. A key that is not in the row's file is never
  rendered: `command/models` shows remote, provider, and baseUrl —
  the retired `concurrency` rides no column.

### 2. The provider's hosted behavior

`provider/openai` gains `NewWithConfig(Config)`; the existing
constructors delegate with defaults and local-row behavior unchanged
(no key, no 429/5xx retry beyond the empty-5xx case,
`reasoning_content`).

- **Auth**: when `Config.APIKey` is set, the request carries
  `Authorization: Bearer <key>`. The key is never part of a fault
  message, a stream line, or a wire test golden.
- **Retry**: a 429 or 5xx response is retried up to `Config.Retries`
  with backoff `base * 2^attempt * (1 + jitter)`; `RetryBase` and
  `Jitter` are Config fields so a test is deterministic and fast. The
  retry re-posts the identical request body inside the same stream
  goroutine; the loop sees no event until the bound is exhausted, then
  the existing loud fault. 4xx other than 429 faults immediately.
- **The empty 5xx before the first token**: a 5xx whose body is empty is
  the proxy's proof that nothing reached the model (llama-swap's
  keep-alive race with llama-server, which a local row can hit) — the
  case is retried for every row, hosted and local, under the same
  backoff with the hosted 3-retry bound as the local default. The retry
  lives at the status gate before the stream; once a streamed byte has
  arrived a failure is never retried, and a 5xx carrying a body is a
  real error — a local row faults it immediately.
- **Cost**: `usage.cost` (a number, dollars) becomes
  `core.Usage.Cost`; `stream_options.include_usage` already asks for
  the usage chunk.
- **Reasoning**: the row's `reasoning` names the wire fields. The
  default (`reasoning_content`) is unchanged: `delta.reasoning_content`
  in, `reasoning_content` on the assistant message out. The OpenRouter
  style reads `delta.reasoning` and `delta.reasoning_details` (the
  array accumulates across chunks, in order) and echoes both under
  those names on later turns. `core.ReasoningDelta` gains an optional
  `Details` raw JSON field and `core.Message` gains
  `ReasoningDetails`, carried by the loop so a later turn's request
  rebuilds the full block.
- **Remote rows**: `chat_template_kwargs` is omitted (the row's effort
  still rides the top-level `reasoning_effort`). An OpenRouter row
  sends `provider: {"order": [<pin>]}` when `providerPin` is set, and
  `cache_control: {"type": "ephemeral"}` when `cacheControl` is true
  (OpenRouter's top-level automatic prompt-caching switch).

### 3. The cost column

`core.Usage.Cost` rides every usage-bearing event (`Done`, `EmptyTurn`,
`Compacted`). The state store's `usage` table gains `cost REAL NOT NULL
DEFAULT 0` (schema v5, presence-keyed migration); `RecordUsage` and
`AddUsage` take it; `UsageRow.Cost` and `SessionUsage` return it.
`SessionCost(ctx, db, sessionID)` sums one session's cost column, and
the sessions list and the TUI footer show the session's dollars.

### 4. Remote spawns

**Amended by SPEC_WORKERS (2.4.0)**: `Concurrency` and the row's
token flock are retired — a remote row carries no parallelism bound;
the endpoint's own 429 retry is the backpressure. A remote delegate
skips the gate entirely (the swap is never consulted — not even for
the names a failure would name). The rest stands.

`DelegateInput` gains `Remote bool`. A remote delegate skips
`delegateBusy` entirely (the swap is never consulted — not even for
the names a failure would name) and carries no flock: the row's
concurrency token (`delegate:model:<id>:<n>`) and the per-session
slot flock both retired with the slot read (SPEC_WORKERS 2.6.0).
`tool/delegate` resolves the
requested model row through a `Models` seam; the swarm resolves the
worker's row from its table. `RunJob`'s model path does the same for a
remote job row: no busy probe, no flock, and the worker
spawned with `-session-id` so its cost is readable after the fire.

The worker process resolves the row from the shared `models.json`: a
remote row's `baseUrl` wins over the `-base-url` flag, so the delegate
and the runner keep passing the swap URL and the worker ignores it.

### 5. Budgets

- **Swarm**: `swarm start <n> [budget=<dollars>]`; the controller keeps a
  running `spent` from each delegate result's `Cost` (which the
  delegate read from the cost column). Before each claim, `spent >=
  budget` stops the worker with the notice `swarm: budget reached —
  $X.XX / $Y.YY — the swarm stops claiming`.
- **Scheduled jobs**: `budget` on create/update (dollars, ≥ 0, 0 =
  unset; `update` with `-1` resets). The runner sums `runs.cost` for
  the job before a fire; at or over the budget it records a skip
  naming the spend. After a fire it records the run's cost, read from
  the cost column for the fire's worker session.
- The delegate records `Cost` on its ad-hoc run, so `scheduler runs`
  shows what a delegation cost beside its exit.

### 6. The seam wiring

`cmd/rig` resolves the active row's hosted fields into the provider
Config at `buildProvider`; the swarm's `Start` resolves each worker
row; `tool/delegate` and `RunJob` resolve through the models table
seam. The scheduler's `runs` table gains `cost` (schema v7,
presence-keyed migration) and `jobs` gains `budget`.

### 7. The second provider (2.15.0)

`core.Provider` is one method and had one implementer since 0.x.
Claude is reachable through OpenRouter, so 2.15.0's gain is not access;
it is native semantics: explicit `cache_control` breakpoints on the two
blocks the words-are-the-budget work made the bulk of every turn (the
system prompt and the tool table, a cache read a tenth of the price),
native thinking blocks with a token budget instead of a
`reasoning_details` translation, and Anthropic's own stop reasons and
usage (`cache_read_input_tokens`, `cache_creation_input_tokens`)
feeding the cost column. And it is the first test of the seam's claim:
a new behavior is a new implementer at the root, never a branch in the
caller — the diff touches `provider/anthropic`, `models`, `config`,
`cmd/rig/root.go` and docs, and `loop/`, `core/` and the policy take
zero lines. The design note is `docs/history/ANTHROPIC-PROVIDER.md`;
the package contract is `provider/anthropic/PACKAGE.md`.

- **The row**: `models.Model` gains `ThinkingBudget` (`thinkingBudget`)
  and four optional per-million prices `inputPrice`, `outputPrice`,
  `cacheReadPrice`, `cacheWritePrice`. `Check` admits `thinkingBudget`
  and `cacheControl` for `provider: "anthropic"`, refuses
  `providerPin` and `reasoning` on it by name (the pin is OpenRouter's
  upstream order; `reasoning` names an openai-compatible wire field
  and the thinking blocks carry their own), refuses a `thinkingBudget`
  at or past the row's `maxTokens` (the api requires room to answer),
  and under the api's own 1,024 floor (the api rejects smaller
  budgets), and refuses the prices on any other provider (openrouter
  reports `usage.cost` itself; a row price there would be a second
  truth).
- **The wire**: `provider/anthropic` posts `{BaseURL}/v1/messages`
  with `stream: true`, `x-api-key` and `anthropic-version` (default
  `2023-06-01`); the key never reaches a fault, a notice, a log or a
  test fixture. The system prompt rides the top-level `system` array;
  the tools ride `tools[]` with `input_schema`; the transcript rides
  content blocks with every `core.ToolCall` id round-tripping as the
  `tool_use` id. The encoder carries only what the api would take: an
  empty tool result rides a named `[no output]` text block (the api
  rejects an empty one, and the result replays on every later
  request); the assistant replay orders thinking, then the text, then
  the tool calls, matching generation; another provider's
  `reasoning.text`-shaped records and an empty thinking record don't
  ride; an assistant turn that then encodes to no blocks is skipped
  rather than sent as `content: null`.
- **The breakpoint choice**: `cache_control: {"type":"ephemeral"}` on
  exactly three blocks — the last system block, the last tool, and the
  last user block of the prior turn. Anthropic's caching docs name the
  mechanics: a cache write happens only at a breakpoint, and a read
  looks backward from a breakpoint for a prior entry inside a
  20-block lookback. The system and tool tables are byte-stable per
  session, so their entries write once and read every turn; the
  transcript breakpoint on the prior turn's tail keeps the writes at
  one per user turn — within a tool round-trip the prefix through the
  breakpoint is unchanged (the assistant's tool_use and the
  tool_results append after it), so each round-trip request re-reads
  the same entry instead of rewriting it at the write multiplier. The
  fourth breakpoint stays unused: three named positions, no fourth
  rule to maintain.
- **The thinking carry-back**: a thinking block streams as
  `thinking_delta` (live `ReasoningDelta`) plus one `signature_delta`;
  the block record — the text and its signature, or a
  `redacted_thinking`'s data — rides `ReasoningDelta.Details` into
  `Message.ReasoningDetails`, the same carrier the openai provider
  uses for reasoning_details. When thinking is enabled the assistant
  message is rebuilt from those records verbatim (the API requires the
  blocks for tool-use continuity, and requires them first in the
  message); when thinking is off no thinking blocks are sent in either
  direction, and a request whose `max_tokens` was lowered under the
  budget — the compact clamp, the summary call — drops thinking for
  that request, thinking blocks included. An empty signature (the
  local Maya runtime's) is carried as it arrived.
- **The cost source**: the row's per-million prices times the usage
  the stream reports — `inputPrice` over the uncached input,
  `cacheWritePrice` over `cache_creation_input_tokens`,
  `cacheReadPrice` over `cache_read_input_tokens`, `outputPrice` over
  the output tokens, zero when the row names no prices. No price
  table in Go. `Usage.Prompt` is the anthropic fields summed
  (`input_tokens` excludes the cached tokens; the sum is what openai's
  `prompt_tokens` means), so the context accounting stays comparable
  across providers.
- **The bounds**: the header timeout (5-minute default, remote rows on
  the bound as `provider/openai` applies them) and the idle bound
  (10 minutes, reset per line) as SPEC_HARDENING 11; a stream without
  `message_stop` is `stream truncated: no finish marker`, the same
  words as openai's; 429 and 5xx retry per `Retries` honouring
  `retry-after`.

## non-goals

- No key management, rotation, or redaction UI: the key is a row field
  or an env overlay and nothing more.
- No cost estimation: only the endpoint's own `usage.cost` is trusted;
  a local endpoint that returns none accounts zero.
- No provider-specific wire dialects beyond the two reasoning styles:
  an unknown provider name is a generic remote row.
- No budget enforcement inside a turn: a budget caps claims and fires,
  not a single in-flight request (a fire may exceed the cap by one
  run; the next claim stops).

## tests

- A fake OpenAI-compatible server: the bearer is sent; 429 (and 5xx)
  retried with backoff (deterministic base and jitter, request count
  and elapsed time asserted); cost parsed and recorded; reasoning
  echoed under the row's field names (OpenRouter's `reasoning` /
  `reasoning_details`, default `reasoning_content`); remote rows omit
  `chat_template_kwargs` and send the pin and the cache switch.
- The cost column: recorded, summed by `SessionCost`, and shown in the
  sessions list and the TUI footer.
- A remote delegate spawn: a fetch seam that fails on any call is
  never called (remote), and the row's concurrency flock is acquired.
- A swarm with a budget: after the recorded cost reaches the cap, the
  controller emits the notice and claims no more.
- A scheduled job with a budget: the fire at the cap records a skip
  naming the spend.
- 2.15.0: a fake Messages server drives `provider/anthropic` the same
  way — the body pinned byte-for-byte including the three breakpoints;
  a tool-use round trip pairing by id; thinking blocks carried back
  with signatures (and redacted_thinking verbatim); every stop reason
  mapped; each error path's words; the truncated stream's fault; 429
  with retry-after; the usage and cost arithmetic from the row's
  prices; a 5-minute header timeout on a silent server and the idle
  bound; the encoder's hard cases pinned failing-first — the empty
  tool result, the budget under a lowered max_tokens, the foreign
  reasoning records, the skipped empty assistant turn, the block
  order, the image bound measured on the encoded bytes. One live smoke
  behind env vars runs keyless against the operator's Messages-format
  llama-swap and proves the wire and the stream against a live server;
  the semantics that server lacks stay pinned by the fakes.
