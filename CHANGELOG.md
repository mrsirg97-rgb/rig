# Changelog

## [0.1.0] — initial release

Shipped through the stacked-review process: each slice landed on its parent,
gated on `go test ./...` / `go vet ./...` / `gofmt -l`, merged down-stack.

- **core + loop** — the typed seams (`Provider`, `Tool`, `Frontend`,
  `ContextPolicy`, `ToolMiddleware`), wire types, the streaming-event
  vocabulary, versioned session JSON with provenance and exec records, the
  composition kernel, and the concrete turn runtime: faults (provider,
  transport, *and* a failing context assembly) surface through `Notify`,
  abort the turn, and leave the session intact; cancelled steps surface
  cleanly; a stream closed without Done or Fault fails loudly.
- **provider/openai** — the SSE streaming adapter over `net/http`: finish-
  marker gated delivery, tool-call accumulation by choice index, tool schemas
  transmitted as JSON objects, non-2xx and truncation surfaced as faults.
- **tool/bash** — `bash(1)` execution with induced-work bounds: 256 KiB
  output cap (truncation named), `WaitDelay` so background children cannot
  hold the turn, and process-group teardown on cancellation.
- **tool/file** — read/write/edit with `FileState` provenance, path-
  normalized drift checking (external modification named and refused;
  ambiguity refused, never guessed at), and a 1 MiB read cap.
- **middleware** — `policy` passthrough (day-one `ContextPolicy`); `perm`
  allow-list, deny-by-default at the boundary with attributed denials;
  `guard.Bound` — every call executes exactly once; the model's re-issuance
  of an identical failing call is counted across turns (name plus args
  digest), refused without executing at the bound, and cleared on success.
- **frontend/cli** — the stdin/stdout REPL over the `Frontend` seam: plain,
  greppable rendering; blank lines no-op; Ctrl-C cancels the turn at its next
  boundary and the session survives.
- **cmd/looper** — the composition root: `looper.New(...)` wires every seam
  in one call; configuration via flags or `LOOPER_*` env, no config files;
  `--version` reports the release.
