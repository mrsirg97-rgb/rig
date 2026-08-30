# tool/python: the persistent kernel

One leaf package: pane's python kernel, ported. One persistent IPython kernel
per session; state (variables, imports, defs) survives across calls, so the
model pays the setup cost once and does cheap incremental work against live
objects. Stdlib only: `os/exec` and the kernel's JSON-lines protocol over
stdio; no third-party Go client.

## goals

- One persistent kernel per session: state survives across calls.
- Pane's surface verbatim: description, promptGuidelines, schema, and every
  runtime voice (timeout, death note, `[stderr]`, `(no output)`, ...).
- Timeout kills the whole kernel and says so: an unexpected death is
  announced on the next call; once, with exit description and stderr tail.
- Process-group discipline from tool/bash: `Setpgid`, `WaitDelay`, group
  kill on teardown.
- Output capped by the host, truncation named (`... [N chars elided] ...`).

## non-goals

- No jupyter_client/ZMQ, multi-client, heartbeats, or side channels. One
  local agent, one stdin pipe, one JSON object per line.
- No loop change: no new events, no middleware, no hooks. Kernel teardown
  until deliverable 7's hooks is one `Close()` call at the root.
- No new dependencies. `go.mod` is unchanged.
- No parallel tool execution (the loop stays sequential in v1): the kernel
  client is still safe under concurrent `Exec` (queue plus id routing),
  which pane's tests exercise directly.
- No Go-side output cap beyond the host's: the wire protocol already
  clips every stream.

## layout

```
tool/python/
  python.go         the tool surface + the kernel client (queue, protocol,
                    death semantics), stdlib only
  kernel_host.py    the embedded host: pane's kernel/kernel_host.py,
                    verbatim (MAX_OUT clip, new_shell, vars, reset, ping)
  python_test.go    pane's named cases, in pane's order, against a real
                    kernel; fake-host cases need no IPython
```

`core/`, `loop/`, `middleware/`, `policy/`, `provider/`: untouched.

## interfaces

`core.Tool` as-is: `Name() "python"`, `Description()` (pane's voice),
`Schema()` (pane's parameters, hand-written), `Exec(ctx, args)`. Plus one
lifecycle method the root calls on the way out; pane's
`session_shutdown` hook with no hook yet:

```go
type Tool struct{ /* owns one kernel */ }
func New() *Tool                      // pane's defaults: venv interpreter, host, lazy bootstrap
func NewWith(python, host string) *Tool  // the injection seam (pane's constructor opts): no bootstrap
func DefaultHost() string             // the host resolution, named (RIG_PYTHON pairs with it)
func (t *Tool) Host() string          // which host a session runs on; the root logs it
func (t *Tool) Close()                // teardown: group kill, bounded wait
```

`NewWith` is what pane's constructor options are for: tests drive it with
fake hosts, and any composer that wants a different interpreter or host
registers one without touching the kernel. The seam provides everything,
so it does not drag in the default venv's lazy bootstrap; `New()` is the
default path and keeps it.

## decisions

- **One kernel per tool instance, not a registry.** rig runs one session
  per process (`main`, the `-p` worker), and the root wires one tool per
  process, so per-instance ownership is exactly pane's one-kernel-per-
  session with no shared package state. A registry would be shared mutable
  state the design test exists to keep out of leaves.
- **The host is embedded and materialised.** `kernel_host.py` ships inside
  the binary (`//go:embed`); `New()` resolves pane's order; pane's
  installed path `~/.pi/agent/kernel/kernel_host.py` if present (interop),
  else the embedded source written to `cfgDir/rig/kernel/kernel_host.py`
  (idempotent, temp+rename). The binary stays single-file; the host stays
  a file the operator can read and the process can be seen running. The
  root logs the choice to stderr at startup (`rig: python kernel
  host: ...`), one line, so pane's and rig's hosts cannot drift
  silently.
- **The interpreter is pane's shared venv.**
  `~/.pi/agent/kernel-venv/bin/python`; numpy and pandas live there. If it
  is missing, the first call bootstraps it lazily, single-flight:
  `python3 -m venv` then `pip install ipython numpy pandas`, 300s per step,
  the failure re-tryable on the next call (pane clears its bootstrap
  promise on failure), voice `kernel bootstrap failed (needs python3 +
  network): ...` verbatim. Stdlib only: it is `os/exec`, not a Go dep.
  The bootstrap is the default path's policy, not the seam's: a silent
  pip-install on first use wants an explicit out, so `main` reads
  `RIG_PYTHON` and passes the operator's interpreter to `NewWith`:
  an explicit choice is an explicit choice, and the bootstrap is
  skipped with it.
- **Queue, then dispatch.** One dispatch at a time (pane's promise chain).
  The timeout timer starts only after the slot is taken, so queue time is
  never charged to the cell's own timeout (pane's named case).
- **Deaths are owned by the exit observer, deliberately.** An unexpected
  exit records a one-shot death note (exit description + stderr tail,
  capped at 4096 chars) that the next dispatch takes exactly once; the
  dying call itself already saw `kernel exited (code|signal N)` with the
  stderr tail. A deliberate teardown (timeout restart, `Close`) nulls the
  current process before killing, so the exit observer sees a foreign
  process and leaves no note; pane's exact `isCurrent()` trick.
- **Protocol state is per process.** The line buffer, the pending-id map,
  and the stderr tail live on the process object, so a restart is a fresh
  object: no stale buffer can swallow the next kernel's reply, and no dead
  kernel's stderr can leak into the next death message. The two fake-host
  cases are structurally true, not tested-into-shape.
- **First writer wins, once.** Replies and death announcements both
  deliver into a buffered-1 channel with non-blocking sends; whichever
  happens first answers the id, the other is dropped. A call is never
  answered twice and a reply is never lost by a late write.
- **Process-group discipline.** `Setpgid` on spawn: `WaitDelay` (2s)
  bounds the pipe wait when a cell's background descendants hold the pipes
  after the host exits; teardown kills the group (`-pid`, SIGKILL), never
  just the leader. The discipline of tool/bash, applied to a child that
  outlives its calls.
- **Unwritable stdin fails fast.** A write to a dead kernel's pipe
  (EPIPE) returns `kernel is not writable: ...` immediately; the timeout
  is not waited out (pane's named case).
- **Cancellation gives up the reply, not the kernel.** A cancelled ctx
  drops the call's pending id and returns the context error; the cell may
  still finish inside the kernel and state survives. The timeout is the
  only deliberate restart trigger, plus deaths.
- **Voices are pane's verbatim**, including the rounding: the timeout
  message rounds ms to seconds half-up (1500ms reads "2s") and says the
  kernel will be restarted on the next call with all variables gone.
  Render order is note, out, `[stderr]`, `[error]`, then result only if
  the out does not already contain it; empty renders as `(no output)` /
  `(failed, no output)`.
- **The host is pane's host.** Same shell setup (text/plain formatter,
  NoColor, Minimal tracebacks, MPLBACKEND=Agg), same `vars`/`reset`
  semantics, same 16000-char clip per stream with the named elision
  marker. One word moved in the docstring ("pi session" -> "agent
  session"); nothing else.
- **The action vocabulary is closed, and `code` is in it** (amended
  2026-08-21). `code` (or an omitted action) runs the code; `vars` and
  `reset` are the host's commands; any other action is refused before the
  kernel is touched, naming the three. The field failure that forced it:
  a model sent `action: "code"` with its code on every call, the old
  dispatch forwarded the unknown cmd without the code, the host's
  fallthrough ran the empty string, and 457 calls in one session came
  back `(no output)` ok; a silent success that ran nothing, the one
  reply shape the harness must never produce. Rejected, named: teaching
  the model out of `code` by description alone (the enum is the teaching;
  the obvious word should work), and refusing `code` (it is the obvious
  word).

## testing

Pane's suite, by name, in pane's order, against a real kernel (the shared
suite kernel, so the order-dependent state cases hold as in pane's file):

- executes code and reports the result
- state persists between calls
- numpy and pandas are importable
- vars lists user-defined names only
- reset clears the namespace
- empty call fails loudly with a clear message
- action code runs the code: an unknown action refuses loud before the
  kernel (added 2026-08-21)
- runtime errors are reported as errors with traceback text
- oversized output is clipped with an elision marker
- hung cell times out, kernel is restarted, caller is told
- parallel calls are routed by id, not corrupted
- a sibling call is not collateral damage when another cell times out
- a queued call's timeout does not count time spent waiting
- an unwritable kernel fails fast instead of waiting out the timeout
- missing host surfaces stderr diagnostics and self-heals
- an unexpected mid-call death is announced to the dying call and the next
  call
- a quiescent death between calls is announced on the next call, once
- a deliberate timeout restart does not produce a death note
- constructor options select interpreter and host (injection seam)
- a dirty death leaves no stale buffer that swallows the next kernel's reply
- a dead kernel's stderr does not leak into the next kernel's death message
- timeout message describes the lazy restart accurately
- NewWith skips the default-venv bootstrap: the default path keeps it

The skip gate: the real-kernel cases skip cleanly when neither the venv
interpreter nor `python3` on PATH can import IPython, numpy, and pandas,
so the suite stays green on a bare box. The fake-host cases (injection
seam, unwritable stdin, dirty death, stderr leak) drive stdlib-only
stand-in hosts through the same seam and skip only when there is no
python3 at all. The bootstrap is a named default-path behaviour (pane's
ensureKernel, ported): a first `New()` dispatch on a box without the
shared venv bootstraps it (python3 + network) before the host starts;
`NewWith` and, with it, `RIG_PYTHON` are exempt.

## scope

One leaf package, one registration line at the root, the allow-list
default growing by `python`, one `Close()` deferred in `main`, one
`RIG_PYTHON` read in `main`, one startup log line. The loop is
byte-identical.
