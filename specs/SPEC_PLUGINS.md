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

- No plugin reload or hot-swap — AMENDED, deliberately reversed by
  decision 8 (the reload and the forge), and GATED: the reversal ships
  only after SPEC_SANDBOX's provenance rule (pending/approve) lands.
  Until then the files are read once at start; a new plugin is a new
  process.
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

// Ecosystem is the plugins tool (8, amended): the one new primitive —
// one mutating native over the ecosystem, an action enum — list (the
// loaded and the skipped, through a root-wired listing seam), create
// (writes a pending plugin, untrusted), delete (moves a loaded plugin
// into plugins/disabled/ — disable, not rm, reversible with /plugins
// enable), reload (re-runs the discovery over the home's plugins/,
// the same loud skips, the same collision refusal, removal free) and
// hands the reports to the root's swap, which takes effect on the
// next turn (never mid-turn). Name is "plugins"; the schema is the
// action enum.
type Ecosystem struct {
	Home    string            // the rig home (the listing is its top-level *.py)
	Kernel  Kernel
	Natives map[string]bool   // the collision set (the native tools, including plugin and plugins)
	Swap    func(ctx context.Context, reports []Report) (string, error) // the root's rebuild
	List    func() (string, error) // the root's listing seam (RenderPlugins)
}
func NewEcosystem(home string, natives map[string]bool, k Kernel, swap func(ctx context.Context, reports []Report) (string, error), list func() (string, error)) *Ecosystem

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
//	Plugins func() []PluginInfo // the loaded and the skipped, in file
//	                    // order — read at call time, so a reload's
//	                    // swap is visible with no re-wiring (8; the
//	                    // slice is the pre-8 shape)
//	Reload  func(ctx context.Context) (string, error) // the reload's
//	                    // action (8): the /plugins reload verb's door
//	                    // and the approve's tail; nil = a pre-8 root
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

`/plugins` was never mutating: no enable/disable (the files are the
interface, as the config's; a change is a new process). AMENDED by
decision 8: `reload` re-registers from disk (a change is a
re-discovery, not a new process), `create <text>` queues the
authoring line, and `approve`'s tail reloads (SPEC_SANDBOX 2).

### 5. The sandbox: named, deferred

**The plugins run with rig's privileges until SPEC_SANDBOX.** They
are python, in the operator's kernel, on the operator's machine: they
can read, write, and exec what the process can. That is the point of
the surface (the model's python tool already is exactly that — a
plugin is the model's python, shipped as a file); the sandbox is a
later spec (a process boundary, a permissions story), not this one's.
One line, as named: **pre-SPEC_SANDBOX, trust the plugins as you trust
your own python.** SPEC_SANDBOX now exists (the worker jail, the
provenance rule); decision 8's reload is gated on it.

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

### 7. The allow-list (AMENDED; the presence reversal)

The reversal, named as SPEC_SANDBOX named its own: the presence of a
plugin in `plugins/` root is itself the allow-list entry. The
provenance rule (SPEC_SANDBOX 2) forces a model's `write`/`edit` into
`plugins/pending/`, so a plugin sitting in `plugins/` root can only
have gotten there by an operator act (the `/plugins` approve) — that
presence IS the admission, and the operator need not add a name to
`allow`.

The mechanism is a second door on the allow-list, wired to the live
plugin table (`middleware/toolset`'s `IsPlugin`): a name is permitted
if it is in the static `allow` **or** the live table carries it as a
plugin (`perm.AllowlistWithDoor`; `Allowlist` is the nil-door today —
static alone). The door speaks for plugins only: a native is never
admitted by it (the collision rule keeps the sets disjoint), and a
plugin in `plugins/pending/` is not live and stays refused until the
operator approves and reloads. A plugin whose file is deleted stops
being admitted on the next reload. The refusal's voice names the tool
and the allow-list; it teaches the shape — a live plugin, or a name
for `allow` — until then. Plugins stay flat as real tools; nesting
them under one tool is a later decision once the count grows.

### 8. The reload and the forge (AMENDED; GATED on SPEC_SANDBOX)

The reversal, named as SPEC_CONFIG named its own: this decision
reverses the "no reload or hot-swap" non-goal. The plugins become a
surface the session can grow: the model authors a plugin (it already
has the `write` tool; the missing primitive is registration without a
restart), the operator asks for one in a sentence, and the running
harness picks it up. **Gated**: nothing here ships until
SPEC_SANDBOX's provenance rule (the pending directory and the approve
verb) is in — a model that can mint its own capabilities with the
operator's privileges is the sandbox's loudest argument, and the gate
is the point of writing both specs together.

The pieces:

- **`plugins`, a native tool** (the one new primitive, amended: the
  `plugins_reload` one is folded into an action-enum ecosystem): re-runs
  the discovery over `~/.rig/plugins/` — the same loud skips, the same
  collision refusal, removal free (the list rebuilds from disk) — and
  swaps the kernel's tool list at the root, the models-switch
  semantics exactly: **next-turn**, never mid-turn (the current turn's
  request already carries its list). The reload imports into the
  running kernel, so a new plugin's functions are callable from the
  python tool immediately, and callable as a tool on the next turn.
  `list` is a management read; `create` writes a pending plugin
  (untrusted, SPEC_SANDBOX); `delete` is disable — it moves a loaded
  plugin into `plugins/disabled/`, reversible with `/plugins enable`,
  one `Move` shared with the `/plugins` command (never an unlink).
- **`/plugins reload`**, the operator's verb: the same re-discovery,
  the same next-turn registration, from the command door.
- **`/plugins create <text>`**, a prompt template on the steer
  precedent (the command queues a line; it never dispatches a turn
  itself): "author a plugin: <text>; the contract is DESCRIPTION,
  SCHEMA, run(args) -> str; write it SELF-CONTAINED to the pending
  directory (SPEC_SANDBOX); call plugins reload; test it with one
  call." The command is sugar over capabilities the model has.
- **Promotion**, the flow that motivated this: kernel code the model
  keeps reusing becomes a plugin on request — and the rule is
  **serialization, not reference**: the plugin file must be
  self-contained, because kernel state dies with the process and the
  file is the persistence. The shared namespace makes the transition
  seamless in-session; the file makes it survive to the next.

The costs, named:

- **The cache**: the tools list rides the request prefix, so a reload
  at deep context is one full re-prefill (minutes on a big window).
  Deliberate, said out loud: a reload is an event, not a tic.
- **The root's seam**: the provider/policy pair already rebuilds
  per-turn (SPEC_COMMANDS 4); the kernel's tool list must be
  re-readable the same way, at the root, zero loop lines — or the
  feature waits. The implementing PR proves this before anything else.
  The seam, named: the loop borrows two things per turn — the
  provider (the request's tools array) and the execution chain (the
  middleware). The root owns both ends of one live tool table
  (`middleware/toolset`): a provider wrapper stamps the table's
  specs into every request before delegating, and a middleware —
  listed first, so innermost, first-listed is innermost — resolves a
  call against the table before the chain's participants bound its
  result, falling through to the loop's own exec for a name the
  table does not carry. A swap (the `plugins`'s, the `/plugins
  reload`'s, the approve's tail) is one atomic write to that table:
  the next turn's request carries the new list and the new tools
  execute, by construction — the models-switch's semantics, zero
  loop lines, core/ and loop/ byte-frozen.
- **Provenance** (the gate, SPEC_SANDBOX): the model's `write` cannot
  land files in `plugins/` directly — model-authored plugins land in
  `plugins/pending/` (invisible to discovery: the listing is top-level
  `*.py` already), and only the operator's `/plugins approve <name>`
  moves one up. The forge mints; the operator blesses.

Rejected, named:

- A file watcher: the reload is an act with a name and a cost, not an
  ambient behavior; watching is machinery and hides the re-prefill.
- Mid-turn registration: the request's tool list is the turn's; a
  list that mutates under a live turn is the race the models-switch
  semantics were designed to avoid.
- Auto-promotion (the harness noticing reuse and promoting on its
  own): the operator asks, or the model proposes and the operator
  approves — a capability that installs itself is the sandbox's
  nightmare shape.

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

**decision 8 (the reload and the forge, the named cases):**

`middleware/toolset` (the seam, pure core — no kernel, no python):

- `TestResolveServesTheTableAndFallsThrough` — a name the table
  carries runs the table's tool (the args and the result, verbatim);
  a name the table does not carry falls through to the inner exec
  (the loop's own, the unknown-tool voice).
- `TestResolveSeesASwapOnTheNextCall` — a tool absent before a Set
  executes after it (the next turn's exec), and a tool the swap
  dropped does not (the list rebuilds from the table — removal free).
- `TestCarryStampsTheRequestPerCall` — the request's tools array is
  the table's specs at call time: a Set changes the next call's
  array, and a call made before the Set keeps the list it was
  stamped with (next turn, never mid-turn).

plugins (the leaf, fake kernel — no python required):

- `TestEcosystemSurfacesAreTheNativeContract` — Name is `plugins`,
  the schema the action enum (list, create, delete, reload), the
  description names the four verbs and the next-turn effect.
- `TestReloadToolExecRediscoversAndHandsOff` — a canned report
  (one loaded, one skipped): the swap receives the reports in file
  order, the reply is the swap's verbatim, and the kernel's cell is
  the discovery's (the files embedded).
- `TestReloadToolEmptyDirectoryNeverStartsTheKernel` — no
  top-level file: zero kernel cells, the swap receives the empty
  report (the list rebuilds to the natives — removal free), the
  reply names the empty list.
- `TestReloadToolCollisionRefusesBeforeTheSwap` — a loaded report
  named like a native (the set includes `plugins` itself):
  the refusal is the startup collision's voice, and the swap is
  never called.
- `TestReloadToolKernelFailureIsTheError` — a non-OK reply: the
  error carries the kernel's reason under the reload's prefix, and
  the swap is never called.
- `TestListIsTopLevelPyOnly` — the listing rule: the pending zone,
  a subdirectory, and a non-.py file are not the listing's, and the
  order is filename order.
- `TestCheckVoicesTheCollision` — the shared refusal (the startup's
  and the reload's one rule): the voice, and a skipped report never
  collides.

command (the leaf, fakes at the Env seam):

- `TestPluginsReloadVerbPassesTheReplyThrough` — the reload seam
  wired: the verb's reply is the root's verbatim; a nil seam
  refuses with the no-seam voice.
- `TestPluginsCreateQueuesTheTemplate` — the queued line is 8's
  template with the operator's text spliced in (the steer
  precedent: the command queues a line and never dispatches a
  turn); the interrupt voice when a live turn was interrupted; a
  nil steer seam refuses; an empty text is usage.
- `TestPluginsUsageNamesTheReloadAndCreateVerbs` — the unknown
  verb's usage line carries the set's whole shape.
- `TestPluginsApproveMovesThenReloads` — the move lands and the
  reply carries the move's line plus the reload's reply; a reload
  failure after the move keeps the move (the disk is the truth) and
  names the failure; a root without the reload seam is the move
  only (the pre-8 voice, intact).

cmd/rig (root + e2e; the fake kernel is the DI seam, the e2e gates
on a usable python as the plugin suite's):

- `TestReloadTakesEffectNextTurnZeroLoopLines` — the seam's proof
  (the feature's gate): a turn over, the root's swap adds a tool,
  and the next turn's request carries it (name, description,
  schema) while the finished turn's request does not; the new tool
  executes on that next turn (the router's end) and the natives
  keep executing (the fall-through); loop/ and core/ stay
  byte-frozen against the branch's base.
- `TestPluginsReloadToolRebuildsTheList` — the model calls
  `plugins` reload: the reply is the reload's (the loud skips in
  it), the next turn's wire carries the new plugin (its
  DESCRIPTION and SCHEMA verbatim) and it executes (the round
  trip); a second reload over a removed file rebuilds the list
  down (removal free), and the wire follows.
- `TestPluginsReloadCollisionRefusesAndKeepsTheList` — a loaded
  report named like a native: the tool error is the collision's
  voice, and the wire is the pre-reload list, whole.
- `TestReloadE2ERegistersAForgedPluginNextTurn` — the real kernel
  (gated): the provider's first request lands a new file in
  `plugins/` (the scripted clock), the model calls
  `plugins` reload, the reply carries the loaded line, the next
  turn's wire carries the plugin, and the model's call of it
  round-trips through the shared namespace (the import is the
  reload's, the call is the next turn's).
- `TestApproveReloadsPost8` — the pending zone's file: `/plugins
  approve` moves it, the reply carries the move plus the reload's
  line, and the next `/plugins` listing shows it loaded (the root's
  state swapped, the command's listing follows).
- the no-plugins wire (the golden pin's companion): the native
  set, `plugins` among them (17, in order), and the golden
  fixtures regenerated in place (the directory is the 0.2.0 wire
  baseline, the bytes the current native set — the pin moves with
  the set, as the earlier releases' did).

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

Decision 8's diffs (the reload and the forge, `0.6.0` → `0.7.0`):

- **SPEC_SANDBOX**: decision 2's usage line gains the two new verbs,
  and the approve's tail is the reload's (post-8).
- **SPEC_CONFIG**: the built-in `allow` default gains
  `plugins` (a native the model must be able to call without
  an operator line), and the golden fixtures regenerate in place.
- **`middleware/toolset`**: NEW leaf (the seam, named in 8's costs).
- **`plugins`**: gains `List`, `Check`, and the `Reload` tool; the
  root's startup collision check is `Check` (one rule, two doors).
- **`command`**: the `reload` and `create <text>` verbs, the
  `Env.Reload` seam, the `Plugins` closure (the listing follows a
  reload), and the approve's tail.
- **`core/` and `loop/`**: byte-frozen (the seam's price, named in 8).

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
