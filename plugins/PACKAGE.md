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

- `Zone(home, dir, zone)`: the files of one zone (`pending`,
  `disabled`) under the home directory named (`plugins`, `train`), the
  same `.py` filter as `List`. Since 2.13.0 the machinery is the rules
  of a kernel-loaded zone, not the rules of a plugin
  (SPEC_PLUGINS 2.13.0, SPEC_DECISION's training section).

- `Discover(ctx, k, files, contract)`: imports every eligible file
  through the kernel and reports each, in file order; the contract's
  checks are generated into the discovery cell. `DiscoverChecked`
  preflights filename validity and native collisions before any
  top-level code executes. `Contract` names what one file of a zone
  must expose: `PluginContract` (DESCRIPTION str, SCHEMA dict, run
  callable) and `TrainerContract` (`train(rows_path, out_dir)` and
  `evaluate(checkpoint, rows_path)` callable); the registry a cell
  populates derives from the contract's kind (`__rig_plugins__`,
  `__rig_trainers__`).
- `Report`: one plugin file's discovery outcome.
- `Tool`: one loaded plugin on the Tool seam.
- `Invoke(ctx, k, kind, name, fn, timeoutMs, args...)`: one call cell
  over a loaded file's method, the args splat from their JSON array and
  the return printed as JSON (2.13.0) — the trainer call
  (`train(rows_path, out_dir)`, `evaluate(checkpoint, rows_path)`) is
  its consumer; the plugin tool's own `run(args)` cell, whose stdout is
  the reply text, stays as it was.
- `Ecosystem` + `NewEcosystem`: the ecosystem arms of the `plugin` door
  (SPEC_PLUGINS 8, amended 2.8.2: the `plugins` native folded into the
  door): one dispatcher over the ecosystem, by `action`:
  `list` (the loaded and the skipped, through a root-wired listing seam),
  `create` (writes a pending plugin, untrusted, through `WritePending`),
  `delete` (moves a loaded plugin into `plugins/disabled/`; disable, not
  rm, reversible with `/plugins enable`, through `Move`), `reload`
  (re-discovery over the home's plugins/ and the hand-off to the root's
  swap). Since 2.14.1 `delete` is the registry's operator verb
  (SPEC_WORKERS 7): a headless menu omits it and `policy/operator`
  refuses it before the door runs — a worker runs, contracts, lists,
  creates and reloads, and never disables a live tool.
- `Move(dir, name, from, to)`: the one file-move shared with the
  `/plugins` disable/enable command: name-voice validation, src/dst
  refusals, the mkdir + rename; never an unlink.
- `WritePending(home, dir, natives, name, source, contract)`: the one
  pending-write shared with the web forge's save: the `PluginNameRe`
  filename-stem rule, the native collision, the contract's attrs read
  off the source text (`def <name>(` for a callable, the bare name
  otherwise), `created` from a prior stat. Each surface keeps its own
  reply voice.
- `PluginNameRe`: the filename-stem rule (`^[a-z][a-z0-9_]{0,63}$`).
- `List(home, dir)`: the named home directory's listing (top-level
  `*.py`).
- `Check`: the collision refusal (a loaded plugin named like a native;
  `plugin` is a native, so it is reserved; `plugins` stays reserved as
  the operator command's name).
- `Kernel`: the shared-kernel seam (one code cell, the host's raw reply).
- `Live`: the live plugin table's seam (SPEC_GROWTH 9): `PluginNames`
  and `Plugin(name)`, implemented by `middleware/toolset`'s Table. The
  lookup admits plugins only: a native named through the door is an
  unknown plugin, so the door never widens the allowlist or skips the
  approval gate, which key on the outer call's name.
- `Plugin` + `NewDoor`: the `plugin` native (SPEC_GROWTH 9, amended): one
  dispatch tool collapsing all plugin schemas to one request entry; an
  `action` enum; `run` (resolves and calls) and `schema` (returns a
  live plugin's description and schema verbatim, the model fetches args
  on demand), both non-mutating. The schema's `name` enum is the live
  plugin names. An unknown name runs the `redo` seam once (the root's
  reload) and re-resolves; a nil redo keeps the plain refusal
  (SPEC_STREAMLINE 4). As of 2.12.6 the door is its interface — the
  struct is unexported and `NewDoor` returns `Plugin` — with one method
  per action: `Run(ctx, name, args)`, `Contract(ctx, name)`, `List(ctx)`,
  `Create(ctx, name, source)`, `Delete(ctx, name)` and `Reload(ctx)`. The
  name-required refusal and the live lookup (redo and all) live in the
  live verbs, the missing-seam refusal in the four forge verbs, which
  call the ecosystem's own typed methods — the ecosystem no longer has a
  JSON door of its own. The schema verb is named `Contract` because
  `Schema()` is the tool's own argument schema, which the door still
  overrides to carry the live plugin names: the one place the template's
  verb-name rule does not fit, named here rather than forced.

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
  newlines and no double-quote collisions. The escaping is total only
  inside a Python single-quoted non-raw literal, so a raw line break
  would end the literal early and turn the embedded data into executed
  code — the cell is refused instead.
- The door's schema keeps `name` a plain string when no plugins are
  live: llama-server rejects `"enum": []` ("enum must be a non-empty
  array"), and a name the server does not know still refuses loudly at
  Exec.
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
- Pending writes and zone moves refuse symlink files: the pending write
  opens with `O_NOFOLLOW` (the Lstat check cannot close the swap race),
  and a zone move re-checks the destination after the rename and rolls
  back a symlink. Provenance checks resolve existing symlinks and the
  deepest existing parent before deciding whether a file is live,
  pending, or foreign.
- `DescriptionOf(path)` / `StaticDescription(src)`: the read-only
  DESCRIPTION read the listings use (no kernel, no execution): a plain,
  single-, or triple-quoted literal, or the parenthesized
  implicit-concatenation form (`DESCRIPTION = ("a " "b")`, comments
  between the pieces allowed); an `f`/`r`/`b` prefix is read as its
  literal text. Seventeen of the operator's twenty-one plugins used the
  parenthesized form and listed as "(no DESCRIPTION)" until this read
  existed; the dashboard and `/plugins` share it.
