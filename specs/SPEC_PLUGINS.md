# rig: the python plugins (the pre-1.0 extension surface)

Python plugins as tools: one file under the rig home's `plugins/`
directory, one tool per file, the name the filename stem. Discovery at
startup through the shared python kernel; registration on the existing
`Tool` seam — a loaded plugin is indistinguishable from a native tool
on the wire. The home moves to `~/.rig` (SPEC_CONFIG 11) and the
`plugins/` directory rides it.

The baseline is 0.3.0 (main at the diff-tool merge, the home amend
included). The invariant: with no `plugins/` directory present, the
wire is 0.2.0's bytes, byte-exact — the `golden_020` fixtures hold
untouched, because a fixture run has no plugins directory and the
discovery is then a no-op that never starts the kernel. `core/` and
`loop/` keep zero diff; the plugins land entirely behind the existing
`Tool` seam.

## goals

- One file, one tool: `~/.rig/plugins/*.py`, top level, the name is
  the filename stem.
- The file's contract: `DESCRIPTION` (str), `SCHEMA` (dict, the tool's
  JSON schema), `run(args: dict) -> str`.
- Discovery at startup through the python kernel host: import each
  file, read the three names, register a tool on the `Tool` seam.
- A file missing a piece, or failing import, is a loud skip naming the
  file and the field — one line, startup continues. A broken plugin
  must not brick the harness.
- A plugin name colliding with a native tool refuses loud at startup
  (native-wins is silent shadowing — refuse instead).
- A call imports nothing new: the kernel invokes the module's `run`
  with the args dict; the return value `str()`s into the tool result;
  an exception is a tool error carrying the traceback tail, never a
  kernel death.
- The kernel is the **same persistent kernel** as `tool/python`, the
  namespace shared — deliberate, named (3): the model's python tool
  can call plugin functions directly, and plugin state persists across
  calls. The cost is accepted and named.
- The `/plugins` command: the loaded plugins (name, description,
  file) and the skipped ones with their reasons. No args, read-only,
  Frontend-side like the rest; the `Description()` ghost like the
  others (no `Sub`).
- The home: `~/.rig`, `$RIG_HOME` over it (SPEC_CONFIG 11) —
  everything moves with it, including `plugins/` and the kernel host's
  materialised directory.
- Pre-1.0, by the roadmap: done when merged; 1.0 still waits on lived
  use.

## non-goals

- No plugin reload or hot-swap: the files are read once at start, as
  the config files; a new plugin is a new process.
- No sandbox: the plugins run with rig's privileges until
  SPEC_SANDBOX (5) — the operator's python, the operator's machine.
- No cross-plugin imports, no manifest, no versioning, no plugin
  dependencies: one file, one tool, three names.
- No new events, no loop change, no middleware, no new provider
  surface: the `Tool` seam only (the loop sees a `core.Tool` instance
  and nothing else).
- No discovery on the `run-job` cold path: `run-job` wires no tools
  and spawns the worker as a `rig -p`, which runs its own discovery in
  its own home.
- No plugin `--` args of their own: the tool call's JSON args dict is
  the argument surface, exactly as a native tool's.

## layout

```
plugins/            NEW leaf (stdlib + core + tool/python, nothing
                    else — the kernel seam, not the loop):
  plugins.go        Report, the Kernel seam, Discover, the per-plugin
                    Tool, the discovery and call cells
  plugins_test.go   the fake-kernel cases (no python required)
tool/python
  python.go         +Run: the raw-reply door the plugins drive (3);
                    the host's materialised directory rides the new
                    home (~/.rig/kernel, SPEC_CONFIG 11)
command
  plugins.go        the /plugins command (4)
  env.go            +Plugins ([]PluginInfo), +the PluginInfo type
  commands.go       All() gains pluginsCmd (the standard set is eight)
cmd/rig
  main.go           the rig home (SPEC_CONFIG 11: $RIG_HOME > ~/.rig,
                    the migration), the plugin discovery and
                    registration before the stores, the /plugins env,
                    Version 0.4.0
specs
  SPEC_PLUGINS.md   this file
  SPEC_CONFIG.md    11: the home (the amend this spec rides)
docs
  SETUP.md, USAGE.md, CHANGELOG.md, ROADMAP.md
```

The design test holds: `plugins/` is one leaf; the root gains a
discovery call and registration lines; `tool/python` gains one
exported door and a moved constant; `command/` keeps its leaf
(deps: core and models only — the plugin rows cross as plain
`PluginInfo`); the frontends are untouched (they dispatch whatever
`command.All()` is and render whatever the tools say). Zero loop
lines. `core/` zero diff.

## interfaces

```go
package plugins

// Report is one plugin file's discovery outcome (2): the name (the
// filename stem), the file, and — when loaded — the description and
// schema the wire carries; when skipped — the reason (the voice).
type Report struct {
	Name        string
	File        string
	Description string
	Schema      json.RawMessage
	Skipped     bool
	Reason      string
}

// Kernel is the shared-kernel seam (3): one code cell, the host's
// raw reply. tool/python's Tool implements it; the fakes stand in
// for it in the tests.
type Kernel interface {
	Run(ctx context.Context, code string, timeoutMs int) (pythontool.Reply, error)
}

// Discover imports every file through the kernel and reports each, in
// file order. A kernel-level failure (start, timeout, a report that
// is not the JSON list) is the error; a per-file failure is a skipped
// report, never the error.
func Discover(ctx context.Context, k Kernel, files []string) ([]Report, error)

// New is the per-plugin tool on the seam: the name is the stem, the
// description and schema are the file's, the call rides the kernel.
func New(name, description, file string, schema json.RawMessage, k Kernel) core.Tool

package command

// PluginInfo is one discovered plugin file (4): the name (the
// filename stem), the description and file when loaded, the reason
// when skipped.
type PluginInfo struct {
	Name        string
	Description string
	File        string
	Skipped     bool
	Reason      string
}

// Env gains:
//
//	Plugins []PluginInfo // the loaded and the skipped, in file order
```

The plugin file's contract (the kernel-side contract, stated once):

```python
DESCRIPTION = "what the tool does, for the model"
SCHEMA = {"type": "object", "properties": {…}}

def run(args: dict) -> str:
    …
```

## decisions

### 1. The shape: a leaf behind the Tool seam

`plugins/` is a leaf (stdlib plus `core` and `tool/python` — the
kernel seam, nothing else) that **discovers and wraps**; `cmd/rig`
**wires**. The root calls `plugins.Discover(ctx, py, files)` once per
process, after the python tool is built and **before any store is
opened** (a collision or a dead discovery refuses loud before the
stores, as the malformed config does), and the loaded tools are
registered through the existing `WithTools` — appended after the
native set, in file order. The loop sees `core.Tool` instances and
nothing else; the middleware chain (the allow-list, the guard) applies
to them as to any tool (7).

The only `tool/python` change is one exported door: `Run(ctx, code,
timeoutMs) (Reply, error)` — one code cell, the host's **raw reply**,
unrendered. `Exec` keeps its rendered voice for the model's tool; the
raw reply is what the plugins need (the `out`/`err`/`error` fields,
individually). No second kernel: both ride the same `*kernel` client,
the same queue, the same process. `command/` stays a leaf over core
and models: the discovery's rows cross into it as the plain
`PluginInfo` struct (the root maps `plugins.Report` →
`command.PluginInfo`), so the command package never names
`plugins/` or `tool/python`.

**No new dependencies.** `go.mod` is unchanged: `encoding/json`,
`context`, `os`, `path/filepath` in the leaf; `tool/python` is an
in-repo seam.

### 2. The file contract and discovery

**The directory.** `~/.rig/plugins/`, **top level only**, `*.py`,
non-recursive; the listing is by filename, ascending (the deterministic
order — discovery, the skip lines, the collision's first refusal, and
the `/plugins` listing all follow it). A directory named `foo.py` is
not a file (silently not listed); a file named `.py` has an empty
stem and is a loud skip (the name is the stem). No plugins directory,
or an empty one, is a **no-op that never starts the kernel** — the
fixture runs (no plugins directory) take the 0.2.0 wire byte-exact
(the invariant), and the kernel stays lazy until the model's first
python call.

**The contract.** The file defines **three things**: `DESCRIPTION`
(str), `SCHEMA` (dict, the tool's JSON schema — the wire's
`function.parameters`), and `run` (callable, `args: dict -> str`).
The discovery cell, executed in the kernel (the first cell — the
kernel starts for it, lazily, exactly as the model's first python call
would start it), imports each file by path (`importlib`'s
`spec_from_file_location`, the stem as module name), validates the
three names (present; `DESCRIPTION` a str; `SCHEMA` a dict; `run`
callable), and keeps the module where the kernel can reach it: the
user namespace's `__rig_plugins__` dict (name → module) and
`sys.modules` under the stem (so `import <stem>` works — the model's
python tool can call plugin functions directly, 3). It prints one
JSON report line (the per-file outcomes); the Go side parses it. The
report rides the cell's stdout like any other cell — the host's
16000-char clip applies (a `SCHEMA` that large is pathological; named,
not guarded).

**The voices, stated once.**

A file **missing a piece** or **failing import** is a **loud skip**:
one line, the file named, the reason named — startup continues, the
other files are still discovered (a broken plugin must not brick the
harness). The reason is the kernel's, verbatim: for a missing piece it
names the field, for an import failure it is the exception.

```
rig: plugins: broken.py: NameError: name 'x' is not defined
rig: plugins: missing.py: TypeError: missing SCHEMA
```

A loaded plugin whose **name collides with a native tool** is a
**loud refusal at startup** (exit 1, before any store): native-wins
would be silent shadowing — the model calls `bash` and the plugin
answers — so the collision refuses instead. The first collision in
file order is named (deterministic, as the config's read order).

```
rig: plugins: name collision: "bash" (bash.py) is already a native tool
```

A **kernel-level discovery failure** (the kernel dies starting, the
cell times out, the report is not the JSON list) is a loud startup
refusal naming the kernel's reason — the discovery's result is
unknown, and fail-closed boots nothing:

```
rig: plugins: discovery: <the kernel's reason>
```

**Silent success.** A loaded plugin prints nothing (SPEC_CONFIG 9's
companion: the load is invisible when it is right); the skips are the
loud ones; `/plugins` (4) is the listing surface.

**The wire.** A loaded plugin is **indistinguishable from a native
tool on the wire**: `name` is the stem, `description` is the file's
`DESCRIPTION` verbatim, `schema` is the file's `SCHEMA` verbatim. The
tools array is the native set (0.2.0's order) plus the plugins in
file order — so with no plugins the bytes are 0.2.0's, pinned.

### 3. The execution: the shared kernel, named

**The call imports nothing new.** A tool call is one code cell in the
**same persistent kernel** as `tool/python`: the cell invokes the
module's `run` with the args dict (the model's JSON args, re-marshalled
compact so the literal the cell carries is total — no raw newlines),
and prints the return. The kernel already has the module (discovery
imported it); the call adds nothing to it.

```python
# the call cell, shaped (the args and the name are the call's)
import json as _rig_j
print(__rig_plugins__["<stem>"].run(_rig_j.loads('<the args, json>')))
```

**The return `str()`s into the result.** `print` is the channel: the
cell prints `run`'s return, and the tool result is that printed text
(the trailing newline dropped). A plugin that prints of its own accord
prints what it prints — stdout is stdout, as in the python tool.

**An exception is a tool error, never a kernel death.** When `run`
raises, the cell's error is the host's `format_exception_only` tail
(the type and the message — the traceback tail), and the tool call
returns it as a tool error the loop feeds back (`name: <tail>`, the
cell's partial output riding along when there is any). The kernel
itself is untouched: the next call — a plugin's, the model's python's
— runs on the same live namespace (the named case, tested).

**The shared namespace is deliberate, and named.** The kernel is THE
SAME persistent kernel as `tool/python` — one process, one namespace:
the model's python tool can call plugin functions directly (`import
<stem>`, or `__rig_plugins__["<stem>"]`), and plugin state persists
across calls, across the model's own python cells, for the life of the
process. That is the point of the seam: a plugin and the model's
scratch work share a world, and the setup cost is paid once.

The cost, accepted and named: **the model can shadow a plugin
mid-session.** A python cell that binds `<stem>` or
`__rig_plugins__` hides or clobbers it for the rest of the session; a
`reset` (the python tool's action) clears the namespace and with it
the plugins — they are the kernel's, not the harness', and a reset is
the kernel's vocabulary. The harness does not defend against it (the
model is a participant, not an attacker, and the sandbox is
SPEC_SANDBOX, 5); the `/plugins` listing names the files, and a
restarted process re-discovers.

**Reset semantics, stated.** `reset` is a fresh namespace: the
plugins' modules and `__rig_plugins__` are gone with the old one, and
subsequent plugin calls fail loud (`NameError: name
'__rig_plugins__' is not defined` — the kernel's own voice). A
`reset` between discovery and first use is the operator's python, not
the harness' bug.

**The stem is the module name.** A plugin's stem is its name in
`sys.modules` (the discovery's `setdefault` — an existing module of
the same name is kept, the plugin's copy stays in
`__rig_plugins__`). A stem that shadows a stdlib name (`json.py`)
shadows it for the kernel's later `import json` in that process: the
operator's file, the operator's choice, named not guarded.

### 4. The /plugins command

The standard set's eighth entry (`command.All()`), one file
(`command/plugins.go`), one registration line — the pattern's own
promise (SPEC_COMMANDS 9). **No args, read-only, Frontend-side** like
the rest: dispatched by the `/` prefix before `Input` returns to the
loop, in the CLI and the TUI alike; the TUI shows the `Description()`
ghost like the other no-`Sub` commands (no argument hints — the
command takes no args). The data crosses as `Env.Plugins` (the root's
discovery rows, mapped to `command.PluginInfo` — `command/` stays a
leaf). The listing is the discovery's, in file order:

```
plugins: 1 loaded, 2 skipped
loaded:
  echo: the fixture echo plugin (/home/u/.rig/plugins/echo.py)
skipped:
  broken.py: NameError: name 'x' is not defined
  missing.py: TypeError: missing SCHEMA
```

Loaded rows carry **name, description, file** (the spec's listing
contract); skipped rows carry **file and reason** (the startup voice,
the same text). Voices:

```
plugins: usage: plugins                                    (args given)
plugins: no plugins seam (the root did not wire one)       (Env.Plugins nil)
plugins: none                                               (no plugins directory, or an empty one)
```

`/plugins` never mutates: no reload, no enable/disable (the files are
the interface, as the config's; a change is a new process).

### 5. The sandbox: named, deferred

**The plugins run with rig's privileges until SPEC_SANDBOX.** They
are python, in the operator's kernel, on the operator's machine: they
can read, write, and exec what the process can. That is the point of
the surface (the model's python tool already is exactly that — a
plugin is the model's python, shipped as a file); the sandbox is a
later spec (a process boundary, a permissions story), not this one's.
One line, as named: **pre-SPEC_SANDBOX, trust the plugins as you trust
your own python.**

### 6. The home

SPEC_CONFIG 11, ridden: the plugins live in `~/.rig/plugins/`, and
the kernel host's materialised directory rides the same home
(`~/.rig/kernel/kernel_host.py`, was `~/.config/rig/kernel/…`). The
resolution is the one rule — `$RIG_HOME` > `~/.rig` — applied at the
two sites that need it (the root; `tool/python`'s host resolution),
both named; the migration (the old home's rename, once) carries the
`plugins/` and `kernel/` subdirectories with it, being a directory
rename. The fixture runs (the `golden_020` e2e) take a scratch `HOME`
with neither home — the migration is a no-op there, and the wire is
0.2.0's bytes (SPEC_CONFIG 9, the companion pinned here: **a fixture
run has no plugins directory**).

### 7. The allow-list

A plugin is **subject to the allow-list like any tool**: the
default-deny below it, the refusal fed back to the model. The
embedded default is the 13 native tools; a plugin is not in it, and
that is deliberate — the operator who installs a plugin adds its name
to `allow` (or `--allow`), and the refusal's voice teaches the shape
until then. Auto-allowing installed plugins would make the allow-list
mean two things (named tools, and named-plus-whatever-is-in-a-
directory); one thing it means is allow-listed tools.

## testing

Named cases, failing first (the standing rule). The fake kernel is
the DI seam: a `plugins.Kernel` stub with canned `Reply`s, so the
discovery/call/voice cases need no python; the e2e cases use the real
kernel behind the same gate as `tool/python`'s suite (a usable
python3 with IPython/numpy/pandas, or a clean skip), a scratch `HOME`
(the fixture-home pattern), and the built binary.

**plugins (the leaf, fake kernel — no python required):**

- `TestDiscoverParsesTheKernelReport` — a canned report (one loaded,
  one skipped): the `Report`s carry the fields (name, file,
  description, schema, the skip's reason), in file order.
- `TestDiscoverKernelFailureIsTheError` — a non-OK reply: the error
  carries the kernel's reason; a report that is not a JSON list: the
  error names the shape.
- `TestDiscoverCellCarriesTheFiles` — the cell the kernel would
  receive: the files embedded (quotes and spaces in a path survive the
  embedding), the three names validated, `__rig_plugins__` and
  `sys.modules` the registration (the cell's text, asserted).
- `TestCallCellIsTotal` — the call cell for args with quotes,
  newlines, and unicode: the re-marshalled JSON is compact (no raw
  newlines), embedded so the cell parses (the text, asserted), and
  the name is the stem's quoted form.
- `TestToolExecRoundTripsArgsAndResult` — a canned OK reply (the
  printed value): the result is the value, the trailing newline
  dropped; the error nil.
- `TestToolExecErrorCarriesTheTracebackTail` — a non-OK reply with
  the exception tail (and partial output): the error is
  `name: <tail>`, the content carries the partial output plus the
  tail (the loop's content-over-error contract).
- `TestToolSurfacesCarryTheFileContract` — `Name`/`Description`/
  `Schema` are the report's (the wire's three, verbatim).

**cmd/rig (root + e2e, scratch homes, the fixture plugins directory —
a good plugin, a broken-import one, a missing-SCHEMA one):**

- `TestRigHomeResolvesEnvOverDefault` — SPEC_CONFIG 11's case (the
  resolution, the no-XDG rule).
- `TestMigrationRenamesTheOldHomeOnce` — SPEC_CONFIG 11's case
  (rename, the marker rides, the line once, the second start silent).
- `TestMigrationNoOps` — SPEC_CONFIG 11's subtests (old absent; home
  present, the old left intact; the failed rename refuses loud).
- `TestRigHomeOverrideBeatsTheOldHome` — SPEC_CONFIG 11's e2e case
  (the override's settings win; the old home intact).
- `TestPluginsDiscoveryRegistersAndSkips` — the fixture directory
  (good, broken-import, missing-SCHEMA): the startup prints exactly
  the two skip lines (file and field named); the request's tools array
  is the 14 natives plus the good plugin (its name, `DESCRIPTION`
  verbatim, `SCHEMA` verbatim); the broken and missing ones are
  absent from the wire.
- `TestPluginCollisionRefusesLoud` — a fixture directory with a good
  plugin named `bash`: exit non-zero, the refusal names the plugin's
  file and the native tool, and no state store is created (the refusal
  is before the stores, 2's position).
- `TestPluginCallRoundTripsArgsResult` — the model calls the good
  plugin with an args dict: the tool result is `run`'s return,
  verbatim (args in, result out — the round trip).
- `TestPluginExceptionIsAToolErrorKernelAlive` — a fixture plugin
  whose `run` raises: the tool result is a tool error carrying the
  traceback tail; the model's next call — the **python tool** — runs
  on the same kernel and returns its result (the kernel is alive
  after, the shared namespace intact).
- `TestRigHomeOverrideWins` — `RIG_HOME` set (a home with a
  `plugins/` directory and a `settings.json`) over the scratch
  `~/.rig` and the old `~/.config/rig`: the run takes the override's
  settings, discovers the override's plugins, and leaves both the
  default home and the old one untouched (the present-home edge, 6).
- `TestNoPluginsDirectoryIsTheV020Wire` — the `golden_020` pin,
  made explicit: a fixture run (no plugins directory) carries exactly
  the 14 native tool names in the tools array, and the request body
  is the 0.2.0 bytes (the existing
  `TestNoUserFilesIsByteIdenticalToV020` subtests, extended with the
  tools-array assertion).

**command (the leaf, fakes at the Env seam):**

- `TestPluginsListRendersLoadedAndSkipped` — Env with loaded and
  skipped rows: the exact rendering (the header's counts, the loaded
  rows' name/description/file, the skipped rows' file/reason, the
  order).
- `TestPluginsNoArgsRefusal` — args given: the usage voice.
- `TestPluginsNilSeamRefusal` — `Env.Plugins` nil: the no-seam voice.
- `TestPluginsNone` — an empty (non-nil) slice: `plugins: none`.

**the set:**

- `TestAllIsTheStandardSet` — the standard set is eight (the existing
  assertion, extended with `plugins`).
- the CLI's unknown-command voice names the eight (the existing
  assertion, extended).

The suite is green on a box with no model loaded and no python: the
leaf cases are fake-kernel, the e2e cases skip cleanly without the
kernel gate, and the golden fixtures are untouched.

## the diffs this spec implies

- **SPEC_CONFIG**: decision 11 (the home: `~/.rig`, the `RIG_HOME`
  override, the migration) — the amend this spec rides; the goals,
  the non-goal's env amendment, the interface comment, and the voice
  examples follow it.
- **SPEC_COMMANDS**: the standard set gains `/plugins` (one file, one
  registration line — 9's pattern, no spec change needed; the
  "seven commands" line's count follows).
- **SPEC_PYTHON**: `tool/python` gains `Run` (the raw-reply door, 1)
  and the host's materialised directory rides `~/.rig/kernel` (6).
- **ROADMAP**: the pre-1.0 item, done when merged (the 1.0 tag still
  waits on lived use).
- **docs/SETUP.md**: the home (`~/.rig`, `RIG_HOME`, the migration),
  the plugins section (the directory, the file contract, the
  discovery's voices, `/plugins`, the allow-list note); the env table
  gains `RIG_HOME`.
- **docs/USAGE.md**: the `/plugins` line.
- **CHANGELOG + Version**: `0.3.0` → `0.4.0`, the
  `TestVersionIsTheFreeze` line updated (additive; pre-1.0 — the
  freeze's discipline and the tag's criterion unchanged).

## scope

What this is not:

- The sandbox (5): a process boundary, a permissions story —
  SPEC_SANDBOX, later. The plugins run with rig's privileges until
  then, and that is the point of the surface, named.
- Plugin reload, manifests, cross-plugin imports, plugin
  dependencies: one file, one tool, three names, read once at start.
- The loop: zero diff through this — the plugins are `core.Tool`
  instances on the existing seam, and if they ever need a loop
  change, the seam was the wrong door and this spec is reopened.
- The TUI: no new surface — the `/plugins` ghost, the command's
  rendering, the tools' rendering are the existing mechanisms.

The runtime becomes 0.4.0: plugin-shaped, still pre-1.0. The loop is
byte-identical; the wire with no plugins is 0.2.0's, byte-exact.
