# rig: hosted mode (remote OpenAI-compatible endpoints)

The provider already speaks the OpenAI wire: plain JSON and SSE over
net/http. A hosted endpoint (OpenRouter, DeepSeek's API, any remote
OpenAI-compatible server) speaks the same wire but needs what a local
llama-server does not: an API key, retry against rate limits, cost
accounting, per-provider reasoning field names, and a remote spawn path
that never touches the local swap. This spec adds exactly that: model
rows gain where they run, the provider gains hosted behavior, cost lands
in the usage column, and swarms and scheduled jobs take dollar budgets.

## what it is not (named)

- **Not a new provider.** No new wire, no new package, no protocol
  translation. The OpenAI-compatible adapter is the provider; hosted
  mode is configuration on top of it.
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
(`baseUrl`), `APIKey string` (`apiKey`), `Concurrency int`
(`concurrency`, default 1 for remote rows), `Reasoning string`
(`reasoning`; `reasoning_content` default, `reasoning` for OpenRouter),
`ProviderPin []string` (`providerPin`, a string or array),
`CacheControl bool` (`cacheControl`), `Retries int` (`retries`,
default 3).

- `Check` refuses a remote row without `baseUrl` (a remote endpoint
  cannot borrow the swap's URL), a `concurrency` below 1, a
  `providerPin` or `cacheControl` on a row whose provider is not
  `openrouter`, and a `reasoning` value outside the two names.
- The env overlay gains `RIG_MODEL_BASE_URL`, `RIG_MODEL_API_KEY`,
  `RIG_MODEL_REMOTE` (bool), `RIG_MODEL_CONCURRENCY`, and
  `RIG_MODEL_REASONING`, so a key can live in the environment and
  never in a file. The overlay wins over the row, like every other
  field; `RIG_MODEL_API_KEY` in a process env is the one place a key
  may live without being in settings.
- `config/modelsfile.go` accepts the new keys and merges them by id
  like the rest. A key that is not in the row's file is never
  rendered: `command/models` shows remote, provider, baseUrl, and
  concurrency but never the key.

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
DEFAULT 0` (schema v4, presence-keyed migration); `RecordUsage` and
`AddUsage` take it; `UsageRow.Cost` and `SessionUsage` return it.
`SessionCost(ctx, db, sessionID)` sums one session's cost column, and
the sessions list and the TUI footer show the session's dollars.

### 4. Remote spawns

`DelegateInput` gains `Remote bool` and `Concurrency int`. A remote
delegate skips `delegateBusy` entirely (the swap is never consulted —
not even for the names a failure would name) and acquires the row's
concurrency token flock (`delegate:model:<id>:<n>` in the scheduler
home) beside the per-session slot flock. `tool/delegate` resolves the
requested model row through a `Models` seam; the swarm resolves the
worker's row from its table. `RunJob`'s model path does the same for a
remote job row: no busy probe, the row's token flock, and the worker
spawned with `-session-id` so its cost is readable after the fire.

The worker process resolves the row from the shared `models.json`: a
remote row's `baseUrl` wins over the `-base-url` flag, so the delegate
and the runner keep passing the swap URL and the worker ignores it.

### 5. Budgets

- **Swarm**: `swarm <n> [budget=<dollars>]`; the controller keeps a
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
seam. The scheduler's `runs` table gains `cost` (schema v6,
presence-keyed migration) and `jobs` gains `budget`.

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
