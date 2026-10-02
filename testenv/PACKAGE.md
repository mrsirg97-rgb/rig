# testenv

## What it is

The suite's isolation from the operator's machine. Every package that
opens config or stores calls `Main` from its `TestMain`, and every
package that dials the swap rides `Transport`.

## What it includes

- `OperatorHome`: the operator's home as captured at package init,
  before `Main` rewrites `HOME`. The fixture probes that read the
  operator's files (the lift checkout, the kernel venv, `.bashrc`) use
  this instead of `os.Getenv("HOME")`, so an isolated suite still finds
  what the machine actually has. The probes are read-only; nothing here
  ever writes into `OperatorHome`.
- `Main`: points `HOME`, `XDG_CONFIG_HOME`, and `RIG_HOME` at one
  throwaway directory for the whole package run, and puts a `crontab`
  shim first in `PATH` that refuses loudly (exit 3, naming the wall), so
  a store migration or a verb that reaches `RealCrontab("")` under the
  suite fails the test instead of editing the operator's crontab. A
  package that needs a working crontab installs its own fake ahead of it
  (`cmd/rig` writes one into the binary's `PATH`). `RIG_HOME` is set empty
  so the rig home resolves from the isolated `HOME`; the Go toolchain
  env (`GOPATH`, `GOMODCACHE`, `GOCACHE`) keeps the operator's caches,
  so the tests' `go build` calls stay warm and offline. `RIG_SWAP_URL`
  is pinned to `ClosedSwapURL` (`http://127.0.0.1:1`, a loopback port
  nothing listens on), so a spawned binary in a test gets the gate's
  fail-closed wiring whatever the host runs; the test helpers in
  `cmd/rig` (`rigEnv`, `pluginEnv`) scrub and re-pin it so a test's own
  fixture always wins. The throwaway home is removed after the run.
- `Server`: the only way a test's httptest server gets dialed. It
  registers the server's host with `Transport` and unregisters it on
  cleanup.
- `Transport`: the test-only dial transport. Any host that is not an
  httptest server created by `Server` is refused before the dial, so a
  test that reaches for the embedded default swap (`127.0.0.1:8090`) or
  any other live endpoint fails loud instead of touching the operator's
  machine. `store/scheduler` installs it on its `Transport` seam from
  its `TestMain`.

## Gotchas

- A package's `TestMain` must call `Main` exactly once; the throwaway
  home is package-scoped.
- `Transport` only refuses `http`/`https` hosts; the unix-socket and
  subprocess paths never go through it.
- `Main` never writes into the operator's home. It does read it: the
  toolchain cache paths are derived from `OperatorHome` when the env
  does not name them.
