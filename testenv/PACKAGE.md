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
  throwaway directory for the whole package run. `RIG_HOME` is set empty
  so the rig home resolves from the isolated `HOME`; the Go toolchain
  env (`GOPATH`, `GOMODCACHE`, `GOCACHE`) keeps the operator's caches,
  so the tests' `go build` calls stay warm and offline. The throwaway
  home is removed after the run.
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
