# cmd/rig

## What it is

The composition root and the binary's entry: every dependency explicit
in one call, wired once at startup. Flags, env, and config files
(SPEC_CONFIG): a four-layer resolution; flags > env > file > embedded
defaults, and the models table lives in the embedded config file. It
is the only package that imports the whole tree; the command package
sees core and models and nothing else.

## What it includes

- **main()**: the REPL path: flag parse, the config load, the rig
  home, the stores (state, rem, todo; one `todo.sqlite`, opened with
  its one-time migration folding the old `<12-hex>.sqlite` files, and
  the one scheduler `global.sqlite`, opened with its one-time migration
  folding the old `cwd-*.sqlite` files), the python
  kernel, the web tools, the plugin discovery, the root's wiring, the
  frontend selection (`-tui` auto / oneshot / plain CLI), the loop, and
  the session closure with the run's exit status.
- **update.go**: the `-update` self-installer (`specs/SPEC_BUILD.md` 5):
  resolves `releases/latest` by the redirect, maps `GOOS`/`GOARCH` to the
  asset, verifies the ed25519 signature against the pinned minisign key
  (`RIG_UPDATE_KEY` > settings.json `updateKey` > the embedded pinned
  key) and the sha256 against `checksums.txt` **before anything moves**,
  and renames a 0755 temp over
  the resolved executable; atomic on one filesystem, a running rig keeps
  its old inode. A missing key, an unsigned release, and a bad signature
  each refuse loud before the old binary is touched; a directory you
  cannot write names itself and the sudo line; a platform with no asset
  and a build with no release tag each say so. Before the rename the
  update drains the room (`drainRoom`): the members are told to leave at
  their turn boundaries and the wait polls `Live()` every
  `drainRoomPoll` (a tenth of a second — the poll is one membership
  read, and the poll cadence only bounds how long a finished drain
  waits to be noticed) for at most `drainRoomBound` (thirty seconds —
  an operator's turn ends long before it, and the update must not be
  held hostage by a wedged member: past the bound the install proceeds
  and prints how many members stayed).
- The minisign wire format is pinned by the decode/verify code and its
  tests (minisign 0.11): every blob carries a 2-byte algorithm tag, then
  the 8-byte key id, then the payload. The public key is `Ed` + key id
  + 32-byte ed25519 key (42 bytes); the signature is `Ed` (legacy,
  covers the file bytes) or `ED` (the default, covers the BLAKE2b-512
  digest) + key id + 64-byte ed25519 signature (74 bytes).
- **runJob** (`runjob.go`): the scheduler verb's cold-shell path, its own
  lifecycle in its own file (own stores, own record — it must not touch
  the REPL's closure order): opens the one
  `global.sqlite`, parses the crontab key `jN` only, its own record, the
  busy policy, the worker jail (SPEC_SANDBOX 1, 5). The session's five
  stores (rem, scheduler, state, todo, decision) open through one
  `openStore` helper: the MkdirAll, the loud open refusal, the
  quarantine line, and the migration report print once, in one shape.
- **root**: the process's mutable wiring state (SPEC_COMMANDS 2): the
  active model, the row, the recorder, the session, the dials; the
  state the command's closures read and rewrite at call time, so a swap
  (new, a resume, a model switch) is visible to every closure with no
  re-wiring.
- **children** (`children.go`): the child operations' effects, the act's
  owner's on its own engine: a start waits the row's port for at most
  `childStartCeil` — five minutes, the longest a cold model load is
  expected to need before its health port answers; past the bound the
  start fails loud, signals the child it spawned, and the log names the
  never-answered port, so a child that lives without serving cannot
  wedge the room's engine's child slot forever —, lands the log at
  `~/.rig/logs/<model id>.log` and
  writes the pid file at the spawn with the process's start time, so two
  starts converge on one child and a stale file names nobody; the
  job-status acts (3.2.0) track their job by the act's origin member
  and publish each one into the instance's local fleet, where
  `root.deliver` hands it to the frontends and the TUI's band row
  breathes it; the room's `member left` notice has the children end
  the departed origin's running jobs as `failed · runner left` on the
  same fleet, so a runner that died without posting its end is ended by
  the room and not by a timeout; `ready`
  is the path a claim takes when its row's port is silent (the start,
  or the swap on the resident row's key, then the wait while `/health`
  says 503); a stop signals the child's
  session, not the shell that forked it, and waits the row's grace
  before the kill; a swap stops the outgoing child first. The room's
  engine stays free while a child starts or stops, and the others learn
  the outcome from the world — the row's port and the pid file.
- **the provider's endpoint** (`root.buildProvider`): a row with a port is
  sent to `http://127.0.0.1:<port>/v1` whatever the base URL says (3.0.2:
  a keyless local row took the base URL, so a worker handed the wire
  front's address claimed, then asked the front, which claimed again
  behind it); a remote row to its own base URL; anything else to the
  base URL.
- **claim** (`claim.go`): the claiming provider every model call rides:
  it records each finished request beside the child's log
  (`room.Record`) and claims the model's key at its class's priority (the operator's turn
  90, a delegate's return 70, a fleet worker 30, a scheduled job 20, an
  outside client 0), the turn waits the admission, and the stream's
  completion is the release. Within one priority the claim that
  continues the session already served goes first; a claim whose row's
  port is silent starts the child (or swaps the resident one out) while
  it holds its place, and sends once the child answers; a member that
  left for an update refuses by name; `rig -update` publishes the update on the table's
  key and drains to its turn boundaries before the binary swaps.
- **The decision wiring** (`decision.go`, the `decisionUrl` block in
  main, `root.frontend`): the decision store opens with the others; the
  gates' recorder holds it and records finals that never fail a call.
  With `decisionUrl` set, one queue proposes — a bash call's risk
  proposal rides a bounded channel, and a landing marks the reviewer
  dirty — and the reviewer reviews at the session's turn end, never
  per proposal. Only an interactive session reviews; a headless worker
  proposes and never reviews. The reviewer's fire is the scheduler's
  `Delegate`, jailed like a swarm worker (the sandbox the operator
  chose) with no tools: it reads the rows and replies on stdout. The
  loop's frontend is the recorder wrapped with the reviewer's turn-end
  wake when one reviews; the wrap stays off `r.fe`, whose `Ask` the
  session reads directly. The train rides the same wiring: `rig
  decision train <trainer>` (`decision_train.go`) runs a trainer from
  the train zone headless, and `/decision train` lands its once-job on
  the crontab through the `DecisionTrain` enqueue seam
  (`command/env.go`), tagged with the rig home the runner reads.
- **The seam closures**: `buildSystem` (prompt assembly), `buildPair`
  (the provider+policy rebuild), `buildProvider` (the row's wire:
  openai, or anthropic when the row says so), `swapIn`, `compactNow`,
  `newSession`,
  `sessionList/Show/Resume`, `switchModel` (the recorder's row write
  happens before the switch applies: a refused write refuses the switch;
  the switch re-swaps the whole live table through `tableTools`, so
  `decide` and `verdict` ride every switch the way the wire and a plugin
  reload do), `switchEffort`, `switchRole`,
  `switchApprove`, `reloadPlugins` / `swapPlugins`, `nativeTools`,
  `runtimeTable`, and the `/rem` command's closures
  (`RemList`/`RemShow`/`RemForget` over `store/rem`).
- **Resolution helpers**: `rigHome` (the migration), `resolveModel`,
  `tuiTrueColor`, `tuiStatusIn`, `tuiNews`, `sessionFor`,
  `checkOneShot`, `splitCSV`, `firstNonEmpty`, `hasLevel`,
  `effortForWire`, `isMutating` (the widened call seam over
  `tool.Mutates`).

## How it is consumed

- Flags carry no defaults (SPEC_CONFIG 2, 5): the defaults live in the
  embedded config file; a passed flag always wins, whatever its value
  (`flag.Visit` reports exactly which were passed).
- `config.Load` resolves the file-over-embedded settings: the env is
  the 0.2.0 empty=unset semantics, except the two presence-aware keys
  (web fetch proxy, trafilatura), for which present is set; even
  empty.
- `wire(r)` assembles the kernel: the provider, the compact+effort
  policy pair, the live tool table (SPEC_PLUGINS 8), and the
  middleware chain from `canonicalMiddleware` (middleware.go, the one
  place the order is written down and the order tests pin it):
  [toolset.Resolve, index (the graph tap, when a code map is open),
  approve.Gate (auto unless a frontend can ask),
  cutoff, perm.Plugins, perm.Allowlist, guard.Bound,
  guard.Rounds, guard.Cap, paths (the `~` boundary, outermost: every
  path is expanded before any validation)]. The order is load-bearing:
  paths expands `~` before any gate validates an argument; cutoff
  refuses a truncated call before approval spends a prompt on it; the
  permission gates deny before approval asks and before the retry guard
  counts a failure; the tap wraps Resolve, not the inside of it —
  Resolve answers a table hit without calling `next`, so a link
  innermost to it never runs, and the tap fires for every call that
  passes the gates and resolves to a real tool, never for a refused
  one; Cap truncates every reply, refusals included. A nil
  allow shuts the plugin door with the allowlist: the fire's worker
  (`-allow none`) executes nothing, plugins included.
  `buildSystem` harvests guidelines from the same constructor, so the
  prompt can never name a chain the tools do not run. Swapping a seam is a
  change there and nowhere else. The compaction `AutoReflect` seam is
  cut: compaction writes nothing to rem (SPEC_COMPACT 6).
- The rem store opens with `remstore.Migration(cwd)` (SPEC_STATE: rem is
  deliberate); the one-time idempotent re-scope and compaction-row
  removal, reported once on stderr. `/rem forget` passes the cwd so the
  store can refuse another project's id.
- The `command.Env` is closures over the root, read at call time: a
  swap is visible with no re-wiring. The Steer seam is the frontend's;
  the dispatcher fills it in its `WithCommands`.
- The frontend: the REPL by default, one-shot under `-p`, the TUI under
  `-tui`, the dashboard over the web door. All four dispatch
  `/`-commands, at the quiet prompt and at re-entry.
- The mesh (3.1.0): settings.json's `mesh` set, `rig serve` constructs
  the door (`machineroom.NewDoor(machine, machineroom.MeshConfig(...),
  remote, home)`) before the web server and closes it on exit —
  `room.MeshConfig` builds `broadcast.MeshConfig` with the tick clock from the
  setting's `period` (milliseconds) and `window`, the door's `Machine`
  and `Addr` the setting's `listen`, the `peers` the other machines'
  doors; the keys the mesh carries split by the row: a remote row (a
  `baseURL`) posts its holds, a row with a port is home; with the setting
  on the front also binds the setting's listen host at the serve port
  beside loopback, the model paths only (`/v1/models`,
  `/v1/chat/completions`), and `/running`, `/unload`, the dashboard and
  its token gate stay loopback. Absent, no mesh
  piece is constructed and 3.0.4's wire is byte for byte.
- `loop.Run(ctx, k)` drives the kernel: interrupt cancels the turn at
  its next boundary. The session row closes with what the run was
  (ok / fault / cancelled).

## The project contract

The system prompt is `system`, the session section, the role's stance,
the operator's `AGENTS.md`, the project's `AGENTS.md`, then the
participants' guidelines. The project's is read at wire from the
session's workspace (`config.ProjectAgents`, the nearest file up to the
repo root), so it follows the session wherever it opens; an unreadable
file is a wire refusal, loud, the same as every other wiring fault.

## The frontend member

The root is the room's frontend member (`rig.MemberFrontend`): it
subscribes once and hands each event to the current recorder, so a
session swap routes to the new one. It delivers what a frontend renders,
`Notice`, `SwarmStatus` and `Phase` (2.11.7), and drops the rest: a
worker's `ReasoningDelta` on the room is the worker's, never the
session's own, and only the member that asked for the work may turn it
into a `Phase`. A notice with no recorder yet goes to stderr.

## Gotchas

- The suite runs under `testenv.Main`: `HOME` and `RIG_HOME` point at a
  throwaway directory for the whole package run, so a fixture that
  forgets its own scratch can never write into the operator's `~/.rig`.
  The operator-home probes (the kernel venv, the config-home migration
  fixtures) read `testenv.OperatorHome` deliberately and never write.
- The wire renders through `TestWireDump` (`go test ./cmd/rig/
  -run TestWireDump -wire-dump <dir> [-rig-root <tree>]`): the three
  request bodies, the tools array, and the menu count, from the fixture
  the old goldens pinned byte-for-byte. `scripts/wire-check` renders two
  trees and posts the diff; there are no stored goldens, no `-update`
  flag, no sha constant, and no regolden commit.
- `run-job` lands before state/session wiring: it is its own lifecycle
  (own stores, own record) and must not touch the REPL's closure order.
- The queue the startup reap sweeps is `sessionQueue`: the session's
  recorded binding when it has one, else the launch directory's. The
  binding is the session row's recorded workspace (the newest
  `state.ListCap` rows this cwd's store carries — a resumed session
  older than that falls back to the launch directory); a row read that
  fails falls back to the launch directory and returns the error, so the
  startup says so on stderr and the reap still runs. A resume from
  another folder therefore frees claims in the queue the session was
  actually working, not in whatever bucket the resume happened to start
  in.
- The startup reap releases todo claims owned by sessions whose rows
  have ended, so a dead session's in-progress tasks return to the shared
  pool at the next open instead of blocking every live session that
  reads the queue. The ended set comes from this cwd's session store; a
  SIGKILL'd session leaves its row open, so its claims age out through
  the todo store's Reap staleness arm. The note names what was freed;
  an idle reap returns "".
- The `-p`/`-resume` conflict is refused loud before any store is
  opened (`ErrResumeWithPrompt`: one-shot stays one-shot). `-p -` reads
  stdin under the read tool's own ceiling (`file.ReadCap`, 1 MiB): a
  longer prompt refuses loud and names the ceiling — a prompt that long
  belongs in a file, and an unbounded read is a wedge an operator's pipe
  can hold the worker on. A piped
  session answers one prompt and exits — there is no next turn to
  carry a delegated worker's return — so the delegate keeps its
  synchronous shape there (`Await` on the piped flag), while the worker
  itself outlives the turn that started it: `Ctx` is the session's
  context, never the turn's.
- `-session-id` is the worker-only identity seam: delegate mints it before
  spawn, and a fresh one-shot records under it instead of discovering a
  concurrent worker's session after the fact. It cannot combine with
  `-resume`.
- The `-exec` door opens only with `RIG_LANDLOCK` set (`execDoor`): the
  landlock spawn's argv and the wrapped tool children re-enter it to
  apply the domain before the exec; a one-shot whose prompt is exactly
  `-exec` stays a prompt.
- The compaction row is resolved loud before the stores: a job whose
  window minus reserve leaves too little to work with fails at start.
- `rigHome` migration is once and deterministic, and a **default-path
  event**: only when `$RIG_HOME` is unset and the new home is absent
  does it rename `~/.config/rig`; under an explicit override the
  migration never runs; a failed rename refuses the start.
- The python plugins: discovery at startup through the shared kernel,
  filename order, the loud skips, then the first collision refusal:
  before the stores. A broken file is a loud skip; a name colliding
  with a native tool refuses loud.
- The TUI's status and news reads are the root's (the stores it already
  opens), at the refresh points only, never per repaint. The TUI's
  status tick runs on the loop goroutine and reads the root's dials
  directly; the dashboard's status poll arrives on a web handler
  goroutine, so `webStatus` takes the dials through a snapshot closure
  on the loop's own engine at the read priority — the same ordering the
  command handlers that mutate the dials run under — and falls back to
  an empty status when the loop is down.
- `runtimeTable` bakes the launch environment's `RIG_MODEL_*` overlay
  into the active row only: a later `/models` switch serves the base row
  from the table, the overlay is the launch environment, not a dial
  (pinned by `TestTheRuntimeTableServesTheBaseRowOnASwitch`).
- `newSession` constructs before it swaps: the chdir and the new
  recorder's `Ensure` run before the old recorder closes, so a chdir or
  Ensure failure leaves the prior session open and active (pinned by
  `TestANewSessionWhoseChdirFailsLeavesTheOldSessionOpen`); the close is
  the last gate and a failure there puts the process back where the call
  found it (`TestProjectRefusesToMoveWhenTheOldSessionCannotClose`); a
  `Close` failure after the `Ensure` can leave the new session's row open
  beside the live one, the retry mints a fresh id.
- The approval dial (SPEC_MODES 4): manual is refused at the command
  when the frontend cannot ask (no ask door); `isMutating` names the
  native pause set plus every plugin (arbitrary python is mutating by
  nature).
- The role change rebuilds the pair because the compact policy captures
  the assembled system; the live turn's request is already built, so
  the stance is visible on the next one.
- One-shot fault propagation: a faulted turn ends the session non-zero
  (the worker's run record derives status from exit); the REPL keeps
  its continue-on-fault semantics.
- The plugin door's redo seam (SPEC_STREAMLINE 4) is the error half of
  the reload; the door needs the swap, not the listing; no home, no
  redo. The door tools hold the live table as their seam, so the root
  builds the empty table first, the doors over it, then fills it from
  the natives (the doors among them) and the plugins.
- `sessions` is the thirteenth of the registry's fifteen entries
  (read-only introspection of the session store, SPEC_STATE). The
  registry answers it a read (`mutates: []`), so it never pauses at the
  gate, and it opens a store, like `todo`/`rem`/`scheduler`, so it is
  not a pure observation;
  `tool/sessions.New(cfgDir, cwd)` is wired in the root's tools map and
  in the embedded `allow` default.
- The worker tools are not fleet-gated (SPEC_WORKERS 5):
  `effectiveNativeNames` is the whole native set — there is always a
  worker model, the resident one or the session's default — and the
  scheduler is wired everywhere. The drain pair (`delegate` and the
  swarm) is a capability, read once at wire time by `fleetWiring` from
  the resident model (`sched.ResidentModel`): the session's model row
  is remote (the gate never consults the local swap), or the resident
  read succeeds — one resident, or nothing resident (the model that
  loads is the session's default) — the pair wires on: the wiring reads
  the same resident truth the gate enforces, so it cannot disagree with
  it. An unreadable swap (the pair fails closed) or settings.json's
  `"workers": false` wires the pair off: the wire happens once, at
  start, and nothing turns the pair on where the swap did not answer. The menu says nothing
  about what is absent; the swarm adapter carries the read's verdict
  and `/swarm` names it when it refuses. `fleetEnds` is the child's
  half of that pipe: a pipe exists because the parent made one, not
  because the child may rule on a review — a delegated worker that
  cannot speak a verdict still sends its heartbeats and its tool calls,
  and only the verdict tool is gated by the allow list.
- `registeredNativeNames` applies the model row's gates on top of that:
  view needs vision, and what is not offered is simply not in the
  table. `applyVision` is the whole of the
  vision gate — the view tool exists when the row has vision and does
  not when it has none — and the model switch re-applies it so the next
  turn's table follows the row. The `serve` path runs the same composition with the web server
  as the frontend (`command.All()`, the `Env`, and the status adapter
  threaded in) and starts the listener beside `loop.Run`; `-p` is
  refused with `serve`.
- The root wires the registry's predicate to the kernel's priority
  function (`WithPriority`), the two named priorities sit in `kernel.go`
  beside the table (a write is 60, a read is 50), and `isMutating` reads
  the one predicate by call (2.17.0, SPEC_WRITES). The system prompt
  carries the standing rules once: the timeoutMs and defer mechanism and the ids
  rule, each said where it cannot drift.
