# plugins

## What it is

The python plugin surface (SPEC_PLUGINS): one file under the rig home's
plugins/ directory, one tool per file, the name the filename stem.
Discovery runs through the shared python kernel; the same persistent
kernel as tool/python, one process, the namespace shared, and the loaded
tools register on the existing Tool seam, indistinguishable from a native
tool on the wire. Stdlib plus core and tool/python (the kernel seam),
nothing else: the leaf discovers and wraps; the root (cmd/rig) wires.

## What it includes

- `Zone(home, zone)`: the files of one zone (`pending`, `disabled`):
  the directories under `plugins/`, the same `.py` filter as `List`.

- `Discover`: imports every eligible file through the kernel and reports
  each, in file order. `DiscoverChecked` preflights filename validity and
  native collisions before any top-level plugin code executes.
- `Report`: one plugin file's discovery outcome.
- `Tool`: one loaded plugin on the Tool seam.
- `Ecosystem` + `NewEcosystem`: the `plugins` native (SPEC_PLUGINS 8,
  amended): one mutating tool over the ecosystem, an `action` enum:
  `list` (the loaded and the skipped, through a root-wired listing seam),
  `create` (writes a pending plugin, untrusted, through `WritePending`),
  `delete` (moves a loaded plugin into `plugins/disabled/`; disable, not
  rm, reversible with `/plugins enable`, through `Move`), `reload`
  (re-discovery over the home's plugins/ and the hand-off to the root's
  swap).
- `Move(dir, name, from, to)`: the one file-move shared with the
  `/plugins` disable/enable command: name-voice validation, src/dst
  refusals, the mkdir + rename; never an unlink.
- `WritePending(home, natives, name, source)`: the one pending-write
  shared with the web forge's save: the `PluginNameRe` filename-stem
  rule, the native collision, the DESCRIPTION/SCHEMA/def run contract,
  `created` from a prior stat. Each surface keeps its own reply voice.
- `PluginNameRe`: the filename-stem rule (`^[a-z][a-z0-9_]{0,63}$`).
- `List`: the home's plugin listing (top-level `*.py`).
- `Check`: the collision refusal (a loaded plugin named like a native;
  `plugin` and `plugins` are natives, so both are reserved).
- `Kernel`: the shared-kernel seam (one code cell, the host's raw reply).
- `Live`: the live plugin table's seam (SPEC_GROWTH 9): `PluginNames`
  and `Plugin(name)`, implemented by `middleware/toolset`'s Table. The
  lookup admits plugins only: a native named through the door is an
  unknown plugin, so the door never widens the allowlist or skips the
  approval gate, which key on the outer call's name.
- `Door` + `NewDoor`: the `plugin` native (SPEC_GROWTH 9, amended): one
  dispatch tool collapsing all plugin schemas to one request entry; an
  `action` enum; `run` (resolves and calls) and `schema` (returns a
  live plugin's description and schema verbatim, the model fetches args
  on demand), both non-mutating. The schema's `name` enum is the live
  plugin names. An unknown name runs the `redo` seam once (the root's
  reload) and re-resolves; a nil redo keeps the plain refusal
  (SPEC_STREAMLINE 4).

## How it is consumed

- `Discover` runs at startup and on reload through `Kernel` (`Run(code,
  timeoutMs)`); tool/python's `Tool` implements it; tests stand in with a
  fake, no python required.
- `New` wraps one discovery report as a `core.Tool`: the root registers it
  alongside native tools.
- `NewEcosystem` is wired as a native tool: the `reload` arm lists,
  discovers, checks collisions, and hands off to the root's swap; `list`
  rides a root-wired listing seam (`command.RenderPlugins`), so the leaf
  never imports the command package.
- The root's swap takes effect on the next turn, never mid-turn (the
  root's table, the loop's per-turn reads).

## Gotchas

- `defaultTimeoutMs` (120000) starts only after the kernel slot is taken:
  a queued call is never charged queue time.
- A kernel-level failure (the call gave up, the kernel died, the report is
  not the JSON list) is the error; a per-file failure is a skipped report,
  never the error; a broken plugin must not brick the harness.
- The discovery cell keeps modules in the user namespace
  (`__rig_plugins__`) and `sys.modules` under the stem, so the python
  tool's imports reach the loaded plugins (the shared namespace). Reload
  replaces both tables together and removes modules no longer live.
- `compactJSON` re-marshals args compactly so the embedded literal is
  total (`pyLiteral`); the args must parse as JSON.
- `pyLiteral` escapes backslash and single-quote: JSON text has no raw
  newlines and no double-quote collisions.
- The doors' redo runs at most once per call and never on a known name;
  a failing redo is named in the refusal (`re-discovery failed: ...`).
  The named cost is one full discovery on the failure path (the retry
  bound still caps a model that keeps calling a name that is not there;
  SPEC_STREAMLINE 4).
- `errorTail` prefers the exception's type+message, else stderr, else a
  named gap.
- `List` skips the pending zone, subdirectories, and non-`.py` files: a
  missing or empty plugins/ dir is a no-op that never starts the kernel.
- `Check` ignores skipped reports (they are not tools): the startup's
  refusal and the reload's are this one rule.
- On a reload, a discovery failure leaves the table and the wire untouched
  (the swap never ran).
- Pending writes and zone moves refuse symlink files: provenance checks
  resolve existing symlinks and the deepest existing parent before deciding
  whether a file is live, pending, or foreign.
- `DescriptionOf(path)` / `StaticDescription(src)`: the read-only
  DESCRIPTION read the listings use (no kernel, no execution): a plain,
  single-, or triple-quoted literal, or the parenthesized
  implicit-concatenation form (`DESCRIPTION = ("a " "b")`, comments
  between the pieces allowed); an `f`/`r`/`b` prefix is read as its
  literal text. Seventeen of the operator's twenty-one plugins used the
  parenthesized form and listed as "(no DESCRIPTION)" until this read
  existed; the dashboard and `/plugins` share it.
