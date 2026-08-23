# cmd/rig

## What it is

The composition root and the binary's entry: every dependency explicit
in one call, wired once at startup. Flags, env, and config files
(SPEC_CONFIG): a four-layer resolution — flags > env > file > embedded
defaults — and the models table lives in the embedded config file. It
is the only package that imports the whole tree; the command package
sees core and models and nothing else.

## What it includes

- **main()** — the REPL path: flag parse, the config load, the rig
  home, the stores (state, todo, rem, the one scheduler
  `global.sqlite`, opened with its one-time migration folding the old
  `cwd-*.sqlite` files), the python
  kernel, the web tools, the plugin discovery, the root's wiring, the
  frontend selection (`-tui` auto / oneshot / plain CLI), the loop, and
  the session closure with the run's exit status.
- **update.go** — the `-update` self-installer (`specs/SPEC_BUILD.md` 5):
  resolves `releases/latest` by the redirect, maps `GOOS`/`GOARCH` to the
  asset, verifies the sha256 against `checksums.txt` **before anything
  moves**, and renames a 0755 temp over the resolved executable — atomic
  on one filesystem, a running rig keeps its old inode. A directory you
  cannot write names itself and the sudo line; a platform with no asset
  and a build with no release tag each say so.
- **runJob** — the scheduler verb's cold-shell path: opens the one
  `global.sqlite`, parses the crontab key `jN` only, its own record, the
  busy policy, the worker jail (SPEC_SANDBOX 1, 5).
- **root** — the process's mutable wiring state (SPEC_COMMANDS 2): the
  active model, the row, the recorder, the session, the dials — the
  state the command's closures read and rewrite at call time, so a swap
  (new, a resume, a model switch) is visible to every closure with no
  re-wiring.
- **The seam closures**: `buildSystem` (prompt assembly), `buildPair`
  (the provider+policy rebuild), `swapIn`, `compactNow`, `newSession`,
  `sessionList/Show/Resume`, `switchModel`, `switchEffort`, `switchRole`,
  `switchApprove`, `reloadPlugins` / `swapPlugins`, `nativeTools`,
  `runtimeTable`, and the `/rem` command's closures
  (`RemList`/`RemShow`/`RemForget` over `store/rem`).
- **Resolution helpers**: `rigHome` (the migration), `resolveModel`,
  `tuiTrueColor`, `tuiStatusIn`, `tuiNews`, `sessionFor`,
  `checkOneShot`, `splitCSV`, `firstNonEmpty`, `hasLevel`,
  `effortForWire`, `isMutating`, `mutatingNatives`.

## How it is consumed

- Flags carry no defaults (SPEC_CONFIG 2, 5): the defaults live in the
  embedded config file; a passed flag always wins, whatever its value
  (`flag.Visit` reports exactly which were passed).
- `config.Load` resolves the file-over-embedded settings; the env is
  the 0.2.0 empty=unset semantics, except the two presence-aware keys
  (web fetch proxy, trafilatura), for which present is set — even
  empty.
- `wire(r)` assembles the kernel: the provider, the compact+effort
  policy pair, the live tool table (SPEC_PLUGINS 8), and the
  middleware chain — [paths (the `~` boundary, innermost), router,
  approve? (when a frontend can ask), perm.Plugins, perm.Allowlist,
  guard.Bound, guard.Rounds, guard.Cap]. Swapping a seam is a
  change here and nowhere else. The compaction `AutoReflect` seam is
  cut: compaction writes nothing to rem (SPEC_COMPACT 6).
- The rem store opens with `remstore.Migration(cwd)` (SPEC_STATE: rem is
  deliberate) — the one-time idempotent re-scope and compaction-row
  removal, reported once on stderr. `/rem forget` passes the cwd so the
  store can refuse another project's id.
- The `command.Env` is closures over the root, read at call time: a
  swap is visible with no re-wiring. The Steer seam is the frontend's;
  the dispatcher fills it in its `WithCommands`.
- The frontend: the REPL by default, one-shot under `-p`, the TUI under
  `-tui`. The REPL is the only frontend that dispatches commands.
- `loop.Run(ctx, k)` drives the kernel; interrupt cancels the turn at
  its next boundary. The session row closes with what the run was
  (ok / fault / cancelled).

## Gotchas

- `run-job` lands before state/session wiring: it is its own lifecycle
  (own stores, own record) and must not touch the REPL's closure order.
- The `-p`/`-resume` conflict is refused loud before any store is
  opened (`ErrResumeWithPrompt`: one-shot stays one-shot).
- The compaction row is resolved loud before the stores — a job whose
  window minus reserve leaves too little to work with fails at start.
- `rigHome` migration is once and deterministic, and a **default-path
  event**: only when `$RIG_HOME` is unset and the new home is absent
  does it rename `~/.config/rig`; under an explicit override the
  migration never runs; a failed rename refuses the start.
- The python plugins: discovery at startup through the shared kernel,
  filename order, the loud skips, then the first collision refusal —
  before the stores. A broken file is a loud skip; a name colliding
  with a native tool refuses loud.
- The TUI's status and news reads are the root's (the stores it already
  opens), at the refresh points only, never per repaint.
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
  the reload — the door needs the swap, not the listing; no home, no
  redo. The door tools hold the live table as their seam, so the root
  builds the empty table first, the doors over it, then fills it from
  the natives (the doors among them) and the plugins.
