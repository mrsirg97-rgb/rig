# provider/openai

## What it is

The OpenAI-compatible streaming adapter over net/http: the wire format is
plain JSON and SSE, stdlib only. Per-model tool-call formats are the
adapter's problem; the loop sees `core.Event` only.

## What it includes

- `New(baseURL, model)`: builds the `core.Provider`. `baseURL` may carry
  a path prefix such as `/v1`; it joins `/chat/completions`.
- `NewWithHeaderTimeout(baseURL, model, headerTimeout)`: the same with a
  dialed time-to-headers bound; `New` applies the 2-minute default.
- `Stream(ctx, req)`: encodes the request, posts, streams SSE, and emits
  `core.Event`s.
- The wire shapes (`wireRequest`, `wireMessage`, `wireTool`,
  `streamChunk`, `wireUsage`, etc.); named types so a field typo fails
  at compile time.
- Helpers: `sseData`, `accumulate`, `sortedPending`, `wireMessages`,
  `wireTools`, `endpoint`.

## How it is consumed

- `New` is wired at the root as the kernel's `core.Provider`: the loop
  calls `Stream` per turn.
- A `unix:` base URL (the runner's socket proxy, SPEC_SANDBOX 1) dials the
  named socket instead: the socket path is the transport's business, and
  the request's path stays the OpenAI suffix, clean.
- `MaxTokens` (0 = omitted, the server default) and `ReasoningEffort`
  (empty = the server default) ride the request; `chat_template_kwargs`
  carries the effort to the chat template.

## Gotchas

- An empty message list is a loud `Stream` error.
- The transport bounds time-to-headers (`ResponseHeaderTimeout`): a
  server that accepts and never speaks faults instead of wedging the
  run, which matters where no interrupt handle exists (the headless
  worker). The stream after the headers is unbounded, so a long
  generation keeps streaming; a `Client.Timeout` would kill it.
- A transport error emits `Fault` (or closes the channel torn-down, with
  no `Done`/`Fault`, when the ctx is dead). A non-2xx status emits `Fault`
  with a response snippet capped at 256 bytes.
- SSE comment lines (`":"`) are the server's keep-alive through a long
  prefill: ignored, never a fault. An unrecognized line or a malformed
  chunk is a `Fault`.
- `usage` is read from the usage chunk (the `stream_options.include_usage`
  request); cached tokens are a subset of `prompt` on this wire, and
  `total_tokens` is read and ignored.
- Tool calls accumulate by index across deltas (`accumulate`): incomplete
  calls are discarded; a missing finish marker faults "stream truncated",
  and a length-capped stream that cut a call's args mid-JSON drops the
  call and faults naming the cause (never poison the transcript).
- The scanner buffer is bounded (64 KiB initial, 4 MiB max).
- Two wire shapes go for the effort when it is set (top-level
  `reasoning_effort` plus `chat_template_kwargs.reasoning_effort)`,
  because two server families read two different fields: OpenAI-shaped
  servers read the top-level; llama.cpp (the worker swap) ignores it and
  its Qwen3 template takes it only as a `chat_template_kwargs` entry.
  A server that knows neither ignores both.
- `parameters` must go over the wire as a JSON object
  (`json.RawMessage`), not a quoted string: OpenAI-compat servers and
  llama.cpp templates take the object shape.
- Without `stream_options.include_usage`, `Done.Usage` is all zeros and
  the cache-hit line reads zero.
