# rig: config loading (the pre-10 runtime component)

Config loading as a first-class runtime component: the knobs that are
today flags, env vars, and constants become a four-layer resolution with
a user file in it, and the models table leaves code for a file. This
deliberately reverses the non-goal SPEC_COMMANDS named — "No config:
the commands are code plus one registration line; the models table stays
8's (code plus env at the root)" — and the header comment in
`cmd/rig/main.go` ("Flags and env only; no config files"). It does so
scope-limited: `core/` and `loop/` keep zero diff (the freeze holds),
a new `config/` leaf parses, the root consumes, and every entry mode
(REPL, `-p`, `run-job`) loads the same way.

The baseline is 0.2.0. The invariant (9): with no user files present,
behavior is byte-identical to 0.2.0, named and tested for every entry
mode. PR A carries this file only; PR B implements on branch `config`,
TDD.

## goals

- One load, every entry mode: REPL, `-p`, and `run-job` resolve config
  the same way, once, before any store is opened or seam wired. The
  worker the runner spawns is a `rig -p`, so it inherits the same load
  in its own cwd — a job inherits its cwd's `AGENTS.md` (6).
- The files, one home: `models.json`, `settings.json`, and `theme.json`
  (reserved) under the config home's `rig/` directory — the same home
  the stores use (`~/.config/rig/` next to `sessions/`, `todo/`, `rem/`,
  `scheduler/`) — plus `AGENTS.md`, global there, project in `<cwd>`.
- The models table out of code: `models.Defaults` becomes an embedded
  default `models.json` (go:embed); the user file merges over it row by
  row; `RIG_MODEL_*` env still wins for the active id (4).
- Rows gain two optional fields: `role` (`worker` | `interactive`,
  default `interactive`, shown by `/models`) and `effort` (the request
  effort where a call sets one: the compaction summary's).
- The existing knobs by their env names, flat: `baseUrl`, `model`,
  `system`, `allow`, `retries`, `python`, `searxngUrl`,
  `webFetchProxy`, `trafilatura`, `swapUrl` — plus `defaultJobModel`,
  the scheduler's job-model default moved by the sweep (5, 8).
- JSON only, stdlib `encoding/json`; a YAML dep is rejected, named (1).
- A malformed or unreadable file is a loud refusal at start naming the
  file and the field; an absent file is silent (3).
- Precedence, one rule stated once and tested: flags > env > file >
  embedded defaults (2).
- `theme.json` is reserved for SPEC_TUI, which owns the palette schema:
  the loader reads it, 10 defines it (7).
- The sweep: every config-shaped value still in code is named MOVED or
  DELIBERATELY KEPT with the reason (8).

## non-goals

- No YAML (or TOML): JSON only, stdlib; the rejection is named (1).
- No per-cwd settings: the cwd carries `AGENTS.md` only.
  `settings.json` and `models.json` are global (the config home).
  Project-level knobs are a 10 candidate, not this.
- No config watching or reload: the files are read once at start. The
  `models` switch reads the runtime table, not the file.
- No `/config` command and no config verbs: the file is the interface;
  the refusal teaches the shape. The TUI (10) may display, over the
  same `Config`.
- No schema versioning in the files: an unknown key is a loud refusal
  (3) — that is the version signal. Adding keys is forward-compatible;
  removing one is a break the refusal names.
- No env expansion in file values: the file is data, no `$HOME`.
- No new env vars: the `RIG_*` surface is unchanged (8); the file layer
  is new, not new env.
- No new `core/` or `loop/` line (10); no new dependencies (1).
- No TUI palette: `theme.json` is read raw; 10 owns its schema (7).

## layout

```
config/               NEW leaf (stdlib + models, nothing else):
  config.go           Load, Config
  settings.go         Settings, the settings.json parse, the per-key
                      overlay over the embedded, the two presence keys,
                      the refusal voice
  modelsfile.go       the models.json parse, the row-by-row overlay
                      over the embedded table
  agents.go           the AGENTS.md pair (global + project)
  theme.go            the theme.json read (raw; 10 owns the schema)
  settings.json       EMBED: the embedded settings — the 0.2.0 flag
                      defaults, moved out of main.go (5)
  models.json         EMBED: the embedded table — the 0.2.0
                      models.Defaults rows, moved out of models/ (4)
  config_test.go, settings_test.go, modelsfile_test.go,
  agents_test.go, theme_test.go
models/               Model +Role / +Effort (4); Check's role
                      vocabulary; Resolve: the RIG_MODEL_* env overlays
                      the active row's fields, the synthesized row
                      carries its role
policy/compact        summarize: the row's effort, "medium" the
                      fallback (4)
command/models.go     renderTable: the role column (4)
cmd/rig/main.go       the load, the per-key chain (2), row resolution
                      over the merged table (4), the scheduler tool's
                      default job model (5), the system-prompt assembly
                      with AGENTS.md (6)
tool/scheduler        New: the default job model (5); the description
                      and schema text built from it
```

The design test holds: `config/` is one leaf; the root gains a load
call and a per-key chain; the leaves touched (`models`, `policy/
compact`, `command`, `tool/scheduler`) gain named fields or one line
each. Zero loop lines. `core/` zero diff.

## interfaces

```go
package config

// Load reads the user files under dir (the config home's rig
// directory, e.g. ~/.config/rig) and the AGENTS.md pair (dir + cwd),
// each merged over its embedded default. Absent files are silent.
// Present-but-malformed or unreadable files refuse loud, naming the
// file and, for JSON, the field (3). Load never creates a file. The
// root computes dir (os.UserConfigDir + "rig", as it does for the
// stores) and passes it: config/ reads paths, not env.
func Load(dir, cwd string) (*Config, error)

type Config struct {
	Settings Settings
	Models   models.Table // file rows over embedded rows, checked (4)
	Agents   string       // global then project, "\n\n"-joined, empty
	                   // segments skipped; "" when neither (6)
	Theme    json.RawMessage // theme.json as written; nil when absent (7)
}

// Settings: the existing knobs by their env names, lowerCamel
// (RIG_BASE_URL -> baseUrl), plus defaultJobModel (no env in 0.2.0;
// file over embedded only). WebFetchProxy and Trafilatura are
// presence-aware: their empty value is a choice (direct egress, the
// stdlib pass) — 0.2.0's documented "set empty" env semantics, extended
// to the file layer (2, 5).
type Settings struct {
	BaseURL         string
	Model           string
	System          string
	Allow           []string
	Retries         int
	Python          string
	SearXNG         string
	WebFetchProxy   *string
	Trafilatura     *string // nil = auto (shared venv, then PATH)
	SwapURL         string
	DefaultJobModel string
}
```

`models.Model` gains (4):

```go
	Role   string // models.RoleInteractive ("interactive", the default)
	           // or models.RoleWorker ("worker"); the /models column
	Effort string // the compaction summary call's request effort;
	           // "" = the policy's "medium"
```

The embedded defaults (the move is exact — 0.2.0's values):

`config/settings.json`:

```json
{
  "baseUrl": "http://127.0.0.1:8090/v1",
  "model": "local",
  "system": "You are rig, a minimal coding agent. Use the provided tools to inspect, change, and run things in the working directory. Answer in plain text when done.",
  "allow": ["bash", "read", "write", "edit", "ls", "find", "grep", "todo", "rem", "scheduler", "python", "web_search", "web_fetch"],
  "retries": 3,
  "searxngUrl": "http://127.0.0.1:8888",
  "webFetchProxy": "http://127.0.0.1:8889",
  "swapUrl": "http://127.0.0.1:8090",
  "defaultJobModel": "qwen3.8-workers"
}
```

(`python` and `trafilatura` are absent: no default, as in 0.2.0.)

`config/models.json`:

```json
[
  {"id": "local", "window": 65536, "maxTokens": 8192, "reserve": 8192, "keepRecent": 16384},
  {"id": "qwen3.8-workers", "window": 65536, "maxTokens": 8192, "reserve": 8192, "keepRecent": 16384}
]
```

(No `role` / `effort`: the field defaults apply — `interactive`, the
policy's `medium`.)

## decisions

### 1. The shape: one leaf, one load, every entry mode

`config/` is a leaf (stdlib plus `models`, nothing else — no `core`, no
store types) that **parses**; `cmd/rig` **consumes**. The root calls
`config.Load(dir, cwd)` exactly once per process, after flag parse and
before any store is opened or seam is wired — the same position
`resolveModel`'s refusal holds today (loud before the stores). The same
call sits on the `run-job` path (which needs the resolved `swapUrl`
before it spawns), and the worker the runner spawns is a plain `rig
-p`, so it runs the same load in its own cwd. One load function, three
entry modes, no per-mode config code.

The config home is the same directory the stores use:
`os.UserConfigDir()` + `rig` (`~/.config/rig` on Linux). The root
already computes it for `sessions/`, `todo/`, `rem/`, `scheduler/`; it
passes it to `Load`. `Load` takes `(dir, cwd)` and reads no env: the
seam is testable in `t.TempDir()` without a scratch `XDG_CONFIG_HOME`
(the e2e pattern stays for the binary cases).

`-version` exits before the load (a version query refuses no config);
every other path loads.

**JSON only, stdlib `encoding/json`; YAML is rejected, named.**
`gopkg.in/yaml.v3` (or the k8s wrapper) would be one new dependency for
a format the operator can already write in JSON; rig's leaves are
stdlib-only by taste (the one non-stdlib leaf dep is the sqlite driver,
decided once in SPEC_STATE); the files are small and structural (a table
and a flat knob set), where JSON's shape is the shape; and the stdlib
gives field-named errors and unknown-key detection natively, while the
YAML libraries' error voices are looser. One format, one parser, no
dep.

### 2. Precedence: one rule, per key

**Flags > env > file > embedded defaults.** Stated once, applied per
key, the same order for every knob and for the models row's fields
(4). A key the operator sets at any layer beats the layers below; an
unset layer descends to the next. Concretely, per key:

```
flag (if set) > RIG_* env (if set) > settings.json (if the key is set
there) > the embedded settings.json value
```

The embedded layer is the 0.2.0 value moved out of code (the
`settings.json` above), so the chain is total: every key resolves to a
value, or to its documented no-default (`trafilatura`: auto), when the
operator set nothing.

**"Set" is defined once, per layer and key.** For **flags**, set means
**passed** (`flag.Visit` reports exactly which were): a flag the
operator typed always wins, whatever its value — `-system ""` runs with
an empty system prompt and `-retries 0` reaches the guard's floor
(clamped to 1), exactly as 0.2.0 behaves. Flags are inherently
presence-aware; defining them by value would silently change both.
For **env and file**, set means **non-empty / non-zero** at that layer
— an empty string or zero descends, exactly as 0.2.0's `envOr` behaves
today — except for two keys that are **presence-aware at every layer**:
`webFetchProxy` and `trafilatura`. For them the
empty value is itself the choice — direct egress, the stdlib text pass —
and 0.2.0 already documents it ("set empty means the variable is
present but empty … presence is the signal, the value is the choice").
For these two, set means **present at that layer** (env: `os.LookupEnv`
ok, even empty; file: the key exists in the JSON, even `""`). This
extends the documented 0.2.0 env semantics to the file layer; the other
keys keep the 0.2.0 env semantics. No key changes meaning between 0.2.0
and this spec — the flag-presence rule above is what preserves that for
explicitly-empty flags.

The rule is tested at every boundary, not re-stated per key (testing):
file-over-embedded in `config`, env-over-file and flag-over-env at the
root, and the presence keys' empty-beats-value case.

### 3. Refusal: loud, file and field; absent is silent

A file that is **present but malformed or unreadable** is a loud
refusal at start, **naming the file and the field** (for JSON) or the
file alone (for `AGENTS.md`), exit 1, before any store is opened. A
file that is **absent** (ENOENT) is silent: the layer simply does not
contribute, the chain descends. No file is created by the load.

The voice is the existing `rig: …` startup voice, with the config
prefix:

```
rig: config: ~/.config/rig/settings.json: retries: expected an integer, got "three"
rig: config: ~/.config/rig/settings.json: unknown key "allowd" (known: allow, baseUrl, defaultJobModel, model, python, retries, searxngUrl, swapUrl, system, trafilatura, webFetchProxy)
rig: config: ~/.config/rig/settings.json: expected a JSON object
rig: config: ~/.config/rig/settings.json: allow[2]: expected a string, got 5
rig: config: ~/.config/rig/models.json: expected a JSON array of model rows
rig: config: ~/.config/rig/models.json: row 2: "id" is required
rig: config: ~/.config/rig/models.json: row 3: duplicate id "local"
rig: config: ~/.config/rig/models.json: row 1: role: "boss" (allowed: interactive, worker)
rig: config: ~/.config/rig/models.json: row 1: window: expected an integer, got "big"
rig: config: ~/.config/rig/models.json: local: Reserve 81920 must be in [0, Window 65536): as large as the window, the trigger fires at every estimate
rig: config: ~/.config/rig/theme.json: invalid character 'x' after object key:value pair
rig: config: ~/.config/rig/AGENTS.md: permission denied
```

Rules that make the voice total:

- **Unknown keys refuse** — the parser decodes against the known key
  set and names the unknown key with the known list (sorted). A typo'd
  key is a silent no-op otherwise; refusal is the fail-closed reading,
  and it is the version signal (non-goal: no schema versioning).
- **The field is the operator's spelling** (the JSON key), not a Go
  struct name: each known key is decoded individually, so a type error
  names `retries`, not `Settings.Retries`.
- **Row errors name the row** (1-based) and the field; post-merge
  invariant errors name the row id and the invariant clause (the
  `models.Check` voice), with the file — the violation is in the merged
  row, and the merged row's file is the one the operator wrote.
- **Read order is fixed** (the first malformed file wins,
  deterministically): `settings.json`, `models.json`, `theme.json`,
  `AGENTS.md` (global, then project).
- `AGENTS.md`: ENOENT is silent; every other read error (permission, a
  directory by that name, I/O) refuses with the OS reason, the path
  named once.

### 4. models.json: the table out of code

**The move.** `models.Defaults` (the `var` in `models/`) leaves code:
the two 0.2.0 rows become `config/models.json` (go:embed, the content
above — the same ids, the same numbers, nothing else). `config/` parses
it with the same parser as the user file, into the same `models.Table`.
The `Defaults` variable is removed; the root and the tests take the
table from `Load` (the test harnesses that name it construct it from
the same rows). The embedded file is the 0.2.0 table, exactly — pinned
by `TestEmbeddedDefaultsAreTheV020Values`.

**The row schema.** A JSON array of row objects, each:

| field      | type   | required | notes |
|------------|--------|----------|-------|
| `id`       | string | yes      | non-empty; unique across the merged table |
| `window`   | int    | yes (new rows) | the row invariants (`Check`) apply after the merge |
| `maxTokens`| int    | yes (new rows) | |
| `reserve`  | int    | yes (new rows) | |
| `keepRecent`| int   | yes (new rows) | |
| `role`     | string | no       | `"worker"` or `"interactive"`; default `interactive`; shown by `/models` |
| `effort`   | string | no       | the request effort where a call sets one: the compaction summary call's; default `""` = the policy's `medium` |

`role` is display and fleet-identity in this PR: `/models` lists it
(4's render); it is validated at parse (unknown value refuses, naming
the allowed set) and stored on `models.Model`. `effort` is consumed by
exactly one call: `policy/compact`'s summary call sets
`ReasoningEffort` to the row's `Effort`, falling back to `"medium"`
when empty — the 0.2.0 behavior, now the field's default. The policy
keeps the fallback line so a row constructed without the default never
loses it; providers that don't know the field ignore it, as today.

**The merge, stated once.** The user file **merges over the embedded
table row by row**, keyed by `id`:

- a user row for an **embedded id**: per-field overlay — each field the
  user set (non-zero / non-empty) replaces the embedded value; each
  field the user left unset keeps the embedded value. A row the user
  writes with only `window` keeps the embedded `maxTokens`, `reserve`,
  `keepRecent`, and `role`.
- a user row for a **new id**: added. The numeric fields are required
  (missing ones refuse, naming them); `role` defaults to
  `interactive`; `effort` defaults to `""`.
- an **embedded row the user file does not list**: kept. The file is an
  overlay, not a replacement.
- the overlay's zero-means-unset has one named cost: a zero value for a
  numeric field is unreachable by overlay on an embedded id (`Check`
  allows `Reserve: 0`, but a `"reserve": 0` descends). A new row can
  carry it; an embedded id cannot. Named as the cost of the rule, not
  worth presence-aware row fields.
- the merged table is then built through `models.New` (each row
  checked, duplicates refused) — a violation refuses with the voice
  above.

**The active id: env still wins.** `models.Resolve` keeps its contract
(given a table, an id, and an env lookup: the row for the id else the
`RIG_MODEL_*` synthesis else a loud refusal), and gains the rule the
precedence chain implies for the row's fields: **for the active id, the
`RIG_MODEL_*` env overlays the row's fields** — `RIG_MODEL_WINDOW` /
`_MAX_TOKENS` / `_RESERVE` / `_KEEP_RECENT`, per field, set beats the
row. 0.2.0 consulted the env only for ids the table did not know; this
makes the env win for a known id too, exactly as the chain says (flags >
env > file > embedded). The synthesis path (unknown id, `RIG_MODEL_
WINDOW` set) is unchanged, except the synthesized row now carries
`Role: interactive`. Rows other than the active one are the table's —
the env overlay is the active id's at startup; `models <id>` switches
read the table (0.2.0's switch semantics).

**The runtime table and `/models`.** The runtime table (what `models`
lists) is the merged table, with the active row replaced by the
**resolved** row when resolution overlaid or synthesized it — so
`/models` shows what is in effect, and `models <id>` can switch back to
it (0.2.0's synthesized-row behavior, generalized). `renderTable`
gains the **role column** after the id:

```
local            interactive  window 65536  max 8192  reserve 8192  keep 16384  trigger 57344  *
qwen3.8-workers  worker       window 65536  max 8192  reserve 8192  keep 16384  trigger 57344
```

File rows list like any others — same columns, same switch, same
refusal voice for unknown ids. The listing order is stable: sorted by
id (the table's `Known()` order), so the golden lines do not depend on
merge order.

### 5. settings.json: the existing knobs, flat, by their env names

One flat object — no nesting — carrying **the existing knobs by their
env names**, lowerCamel of the env minus the `RIG_` prefix:

| key             | env                 | 0.2.0 default (the embedded value) |
|-----------------|---------------------|------------------------------------|
| `baseUrl`       | `RIG_BASE_URL`      | `http://127.0.0.1:8090/v1`         |
| `model`         | `RIG_MODEL`         | `local`                            |
| `system`        | `RIG_SYSTEM`        | rig's default system prompt        |
| `allow`         | `RIG_ALLOW`         | the 13-tool default list           |
| `retries`       | `RIG_RETRIES`       | `3`                                |
| `python`        | `RIG_PYTHON`        | (none: the default interpreter)    |
| `searxngUrl`    | `RIG_SEARXNG_URL`   | `http://127.0.0.1:8888`            |
| `webFetchProxy` | `RIG_WEB_FETCH_PROXY`| `http://127.0.0.1:8889` (presence key) |
| `trafilatura`   | `RIG_TRAFILATURA`   | (none: auto — presence key)        |
| `swapUrl`       | `RIG_SWAP_URL`      | `http://127.0.0.1:8090`            |

Shapes: `allow` is a **JSON array of tool names** in the file (the env
stays CSV — the 0.2.0 env surface is unchanged); the rest are strings
or the integer `retries`. `allow`'s array elements are strings; a
non-string refuses (`allow[2]: …`).

**`defaultJobModel`** — the one key without an env name (the sweep's
move, 8): the scheduler's default job model, today the constant
`"qwen3.8-workers"` in `tool/scheduler` (also interpolated into the
tool's description and schema text) with a fallback copy in
`store/scheduler`. It moves to the embedded settings as
`"qwen3.8-workers"`, overridable by the file. The root passes it to
`tool/scheduler.New` (the constructor gains the parameter); the tool's
description and schema text are **built from the passed value** — with
the embedded value the wire bytes are 0.2.0's, pinned by the invariant
(9). The store's `Create` keeps its constant as the fallback for the
direct path (named, 8); the tool always passes a non-empty model, so
the fallback is a safety net, not a second source. `defaultJobModel`
has no flag or env: its chain is file over embedded, and a job's
explicit `model` arg beats it, as today.

The 0.2.0 flags keep their names and their position in the chain; their
**defaults move** from `main.go` into the embedded file: the flag
defaults become unset (empty / zero), and the chain (2) resolves. The
`-h` text names the chain.

### 6. AGENTS.md: global then project, into the system prompt

**The files and the order.** `<configdir>/rig/AGENTS.md` (global) then
`<cwd>/AGENTS.md` (project), **concatenated global-first** with a blank
line between them (empty segments skipped):

```
<global AGENTS.md>

<project AGENTS.md>
```

The content is the files as written — no markers, no headers, no
indentation: the file is the contract, and the assembled prompt stays
greppable. Absent files are silent (3); the cwd is the process's cwd —
the REPL's cwd, or the worker's job cwd (below).

**The placement, named against the middleware Guidelines.** The root
already assembles `system + "\n\n" + guidelines`, where `guidelines` is
the `core.GuidelineContributor` prose of the middleware participants
(SPEC_HARDENING decision 6's collection — today no participant
contributes; `perm` and `guard` are wrap-only). AGENTS.md sits
**between the system prompt and the participant guidelines**:

```
fullSystem = join( [system, AGENTS.md(global+project), guidelines], "\n\n" )
```

skipping empty segments. The order is descending proximity: the
operator's identity prompt, then the user's project contract (broad to
narrow: global before local), then the participants' operational prose
(machine-contributed, closest to the tool surface). With no AGENTS.md
present the assembly is 0.2.0's bytes exactly (9); the order is pinned
by `TestAgentsOrderAgainstGuidelines` when a guideline participant is
present.

**Every entry mode loads it.** The REPL and `-p` get it in the root's
assembly; **the worker inherits its own cwd's `AGENTS.md`**: `run-job`
spawns the worker with `cwd = job.Cwd`, and the worker is a `rig -p`
that runs the same load — so the job's system prompt carries the global
`AGENTS.md` plus the project `AGENTS.md` **of the job's working
directory**, not the creating session's cwd. The creating session's
project file is the session's context, not the job's; the job's context
is its cwd. (The job's `system` otherwise resolves the same chain the
REPL's does — the worker's own flag/env/file/embedded resolution, the
worker's env being the fire engine's env.)

### 7. theme.json: reserved for SPEC_TUI

`theme.json` is **reserved for deliverable 10** (`frontend/tui`), which
owns the palette schema: **the loader reads it, 10 defines it.** The
loader's contract is total-but-shallow: present → it must decode as one
well-formed JSON value, else the loud refusal (3); the decoded raw bytes
are exposed on `Config.Theme` (`json.RawMessage`), verbatim as written;
absent → `Theme` is nil, silent. The loader validates well-formedness
only — no fields, no keys, no schema: the moment it named a field it
would own 10's territory. 10 decodes `Config.Theme` with its own schema
and its own tests.

### 8. The sweep: what moves, what stays, why

Every config-shaped value still in code, named. **MOVED** = out of code
into the embedded defaults (and overridable by the file layer);
**DELIBERATELY KEPT** = caps, bounds, pinned thresholds, derivation
rules, mechanics, and prompt text — the values that are not operator
knobs.

| value | 0.2.0 location | verdict |
|-------|----------------|---------|
| default system prompt | `cmd/rig/main.go` `defaultSystem` | **MOVED** → `settings.system` (embedded) |
| default base URL `http://127.0.0.1:8090/v1` | `main.go` flag default | **MOVED** → `settings.baseUrl` |
| default model id `local` | `main.go` flag default | **MOVED** → `settings.model` |
| default allow-list (13 tools) | `main.go` flag default | **MOVED** → `settings.allow` |
| default retries `3` | `main.go` `envOrInt` | **MOVED** → `settings.retries` |
| models table rows | `models/models.go` `Defaults` | **MOVED** → `config/models.json` (embedded) |
| summary-call effort `"medium"` | `policy/compact/compact.go` | **MOVED** → the row's `effort`; the policy keeps `"medium"` as the field's default (4) |
| default SearXNG `http://127.0.0.1:8888` | `tool/web/web.go` | **MOVED** → `settings.searxngUrl` |
| default fetch proxy `http://127.0.0.1:8889` | `tool/web/web.go` | **MOVED** → `settings.webFetchProxy` |
| default swap URL `http://127.0.0.1:8090` | `store/scheduler/runner.go` | **MOVED** → `settings.swapUrl` |
| default job model `qwen3.8-workers` | `tool/scheduler` + `store/scheduler` | **MOVED** → `settings.defaultJobModel`; the store keeps its constant as the direct-path fallback (5) |
| row invariants (`Reserve < Window`, `KeepRecent < Window-Reserve`, …) | `models.Check` | **KEPT** — the construction-time brake, not a knob |
| synthesis formulas (`MaxTokens 8192`, `Reserve Window/8`, `KeepRecent Window/4`) | `models.Resolve` | **KEPT** — derivation rules for the `RIG_MODEL_*` path, not values |
| `RIG_MODEL_*` env synthesis | `models.Resolve` | **KEPT** — the operator surface stays; it now also overlays the active row's fields (4) |
| retries floor (`< 1` → `1`) | `middleware/guard` | **KEPT** — bound |
| session list cap `50`, turns formula | `store/state` | **KEPT** — cap + formula |
| runs count `1–100` (default `5`) | `store/scheduler` | **KEPT** — cap + arg default in the tool's voice |
| job store event compaction `1000` | `store/scheduler` | **KEPT** — pinned threshold |
| log prune `20`; digest prefixes (`6`/`12` hex); key shape `cwd-<12hex>:jN` | `store/scheduler`, `main.go` | **KEPT** — mechanics of the store's location and history |
| run timeout `30m`; busy-check fetch `5s`; busy policy; `ReportBack` text; the worker argv shape | `store/scheduler/runner.go` | **KEPT** — bounds, policy, prompt text, spawn mechanics |
| web caps: search `15s`/`300`/`1MiB`; fetch `5MiB`/`20000`/`30s`/`5` hops/`20s`/`10MiB` | `tool/web` | **KEPT** — caps and bounds |
| python: `120s` timeout, `4096` stderr tail, `2s` wait delay, host resolution (`~/.pi/…` shared path, then the embedded materialisation) | `tool/python` | **KEPT** — bounds + mechanics; only the interpreter knob moves (`python`) |
| compact: calibration clamp `0.5–4.0`, the `min(Reserve/4, 256)` floor, split math, the summary prompt file | `policy/compact` | **KEPT** — pinned thresholds and the fold contract |
| rem `AutoReflect` importance `0.2` | `store/rem` | **KEPT** — pinned threshold |
| `RIG_FAKE_STATE_DIR`, `RIG_FAKE_MODE` | `cmd/rig` tests | **KEPT** — test seams, not operator config |
| the `RIG_*` env set | `main.go`, `models`, `tool/*` | **KEPT** — unchanged as a surface (no new env vars); each knob gains a file layer below it |

### 9. The invariant: no user files, 0.2.0 byte-identical

**With no user files present — no `settings.json`, no `models.json`, no
`theme.json`, no `AGENTS.md` anywhere — every entry mode behaves
byte-identically to 0.2.0.** Stated once, tested per entry mode:

- **REPL**: the request body sent to the provider for a fixed prompt is
  the 0.2.0 bytes — the system message (the default system prompt, no
  AGENTS.md, no guidelines participant), the model, the tools spec
  (including the scheduler description's `Default model:
  qwen3.8-workers`), the messages. A golden fixture pins the exact
  bytes (`TestNoUserFilesIsByteIdenticalToV020/repl`).
- **`-p` one-shot**: the same, over the one-shot path (subtest
  `oneshot`); stdout is the assistant text only, as 0.2.0.
- **`run-job`**: the worker's **argv** is 0.2.0's
  (`-p <prompt+ReportBack> -base-url <swap>/v1 -model <job model>`),
  and the worker's request body is the 0.2.0 bytes (subtest `runjob`).
- **The compaction wire**: the summary call carries
  `reasoning_effort: "medium"` in both shapes, as 0.2.0 (the embedded
  rows carry no `effort`; the policy's default applies).
- **The refusals**: the unknown-model-id refusal names the same known
  ids and the env, as 0.2.0.
- **The named exception**: `/models` gains the role column (4) — a new
  feature on a new surface, not part of the 0.2.0 wire; the existing
  `/models` assertion updates with the column.

Two companions: **an empty config directory** (the directory exists,
no files) is silent — the same bytes as the absent case; and **success
prints nothing** — the load is invisible when it is right (the
`rig: python kernel host:` line stays, as 0.2.0).

### 10. The freeze and the touched surface

- **`core/` and `loop/`: zero diff.** The freeze holds: no event, no
  wire type, no seam, no loop line. The config is root-owned state the
  loop never reads — exactly as the models table is today.
- **No new dependencies**: `encoding/json`, `go:embed`, `os`,
  `path/filepath` — stdlib.
- **Touched, named**: `config/` (new leaf + the two embedded files),
  `models/` (`Model` +`Role`/`Effort`, `Check`'s role vocabulary,
  `Resolve`'s env-overlay and synthesized-row role, `Defaults`
  removed), `policy/compact` (the summary call's effort, one line plus
  its comment), `command/models.go` (the role column),
  `tool/scheduler` (`New`'s default-job-model parameter, the
  description/schema built from it), `cmd/rig` (the load, the chain,
  the row resolution, the assembly, the tool wiring), the tests that
  name `models.Defaults` (they take the table from the same rows).
- **The RIG_* env surface is unchanged** — every env var means exactly
  what it meant in 0.2.0, with a file layer below it (8).
- **Version**: PR B bumps `0.2.0` → `0.3.0` (additive; the runtime is
  still pre-1.0 — the freeze's discipline, the tag's criterion,
  unchanged) and updates `TestVersionIsTheFreeze` with it.

## testing

Named cases, failing first (the standing rule). Fakes at the DI seam:
`config.Load` over `t.TempDir()` (no env, no binary), a scripted
`httptest` provider for the wire, real stores in `t.TempDir()` where a
case names one, the built binary for the e2e.

**config — the parse and the overlay:**

- `TestLoadAbsentFilesIsSilent` — no dir, no files, no AGENTS.md:
  `Config` with the embedded values, `Theme` nil, `Agents` "".
- `TestLoadEmptyDirIsSilent` — the dir exists, no files: the same
  result (the directory's presence is not an event).
- `TestEmbeddedDefaultsAreTheV020Values` — the embedded settings equal
  the 0.2.0 flag defaults key by key (baseUrl, model, the system text,
  the 13-tool allow, retries 3, searxng, proxy, swap); the embedded
  table equals the 0.2.0 `models.Defaults` row by row (ids, window,
  maxTokens, reserve, keepRecent) — the move is exact.
- `TestSettingsMalformedNamesFileAndField` — subtests pinning each
  voice from 3: the retries type, the unknown key (the known list,
  sorted), the top-level not an object, the allow element.
- `TestSettingsZeroDescendsToEmbedded` — `"retries": 0`,
  `"model": ""`: the embedded values (zero = unset, 2).
- `TestSettingsPresenceKeysInFileAreExplicit` —
  `"webFetchProxy": ""` → direct (the empty choice beats the
  embedded); `"trafilatura": ""` → set empty; the keys absent →
  the embedded / nil (2, 5).
- `TestModelsMalformedNamesFileRowAndField` — subtests: the top-level
  not an array, a row not an object, the missing id, the duplicate id,
  the unknown role, the bad int, the unknown row key.
- `TestModelsMergesOverEmbeddedRowByRow` — a user row for `local` with
  only `window`: the user's window, the embedded maxTokens/reserve/
  keepRecent/role; a new row `brain` (full numbers): added,
  `role interactive` (the default), `effort ""`; the unlisted
  embedded row kept (4).
- `TestModelsMergeViolationRefuses` — an overlay that breaks
  `Reserve < Window`: the refusal names the file, the id, the clause.
- `TestAgentsGlobalThenProject` — global `G` + project `P` →
  `"G\n\nP"`; `TestAgentsProjectOnly`, `TestAgentsGlobalOnly` (each
  alone, no stray blank line).
- `TestAgentsUnreadableRefuses` — `chmod 000` (skipped when running
  root): the voice names the path; `TestAgentsDirectoryRefuses` — a
  directory named `AGENTS.md`: the `is a directory` refusal.
- `TestThemeAbsentNil`, `TestThemeRawIsTheFileBytes` (the raw
  document round-trips, as written), `TestThemeMalformedRefuses` (the
  decoder's reason, the path).

**models:**

- `TestCheckRoleVocabulary` — `"boss"` refuses (the voice);
  `interactive` / `worker` pass; `""` refuses (the default is the
  caller's, 4).
- `TestResolveEnvOverlaysTheActiveRow` — a table row plus
  `RIG_MODEL_WINDOW`: the window is the env's, the rest the row's (4's
  rule — new behavior, named).
- `TestResolveSynthesizedRowCarriesInteractive` — the unknown-id +
  env path: `Role: interactive`, `Effort: ""`.

**policy/compact:**

- `TestSummaryEffortIsTheRow` — a row with `Effort: "low"`: the
  summary request carries `low` (both wire shapes, the adapter test's
  assertion); a row with `Effort: ""`: `medium` (the 0.2.0 bytes).

**command:**

- `TestModelsListShowsRoleColumn` — two rows (one `worker`), the exact
  lines including the column and the active marker.

**cmd/rig (root + e2e, scratch homes, scripted provider):**

- `TestNoUserFilesIsByteIdenticalToV020` — subtests `repl` /
  `oneshot` / `runjob` against the golden request-body fixtures
  (9): the exact bytes, the worker argv, the refusal voice for an
  unknown model id.
- `TestPrecedenceFlagOverEnvOverFileOverEmbedded` — one key
  (`system`), four runs: each layer wins when the layers above are
  absent (2's rule, tested at every boundary).
- `TestFlagPresenceWins` — `-system ""` runs with the empty system
  prompt (not the embedded default); `-retries 0` reaches the guard's
  floor (the clamp to 1, not the embedded 3): a passed flag wins,
  whatever its value (2's flag rule, the 0.2.0 semantics preserved).
- `TestPrecedencePresenceKeyEnvEmptyBeatsFile` —
  `RIG_WEB_FETCH_PROXY=""` + a file value: direct wins (2).
- `TestRunJobSwapUrlChain` — the file's `swapUrl` reaches the busy
  check; `RIG_SWAP_URL` beats it; neither: the embedded (via the
  scripted busy endpoint).
- `TestRunJobWorkerInheritsJobCwdAgents` — a job cwd with
  `AGENTS.md` (`JOB`) and a session cwd with `AGENTS.md` (`SESS`):
  the worker's system message carries `JOB` and the global, not
  `SESS` (6's worker semantics, named).
- `TestAgentsOrderAgainstGuidelines` — a root with a
  guideline-contributing middleware plus both AGENTS files:
  `fullSystem` is `system + "\n\n" + agents + "\n\n" + guidelines`
  exactly (6's order, pinned); the existing
  `TestGuidelinesAreCollectedIntoTheSystemPrompt` (no AGENTS.md)
  stays green.
- `TestRowEnvBeatsFileForActiveID` — a file row for the active id
  plus `RIG_MODEL_WINDOW`: the in-effect row (and the `/models` line)
  carries the env's window; the file row lists under its id (4).
- `TestDefaultJobModelFromSettings` — `scheduler create` with no
  model: the job row and the reply carry the file's
  `defaultJobModel`; the tool description names it (5).
- `TestMalformedConfigRefusesBeforeStores` — a malformed
  `settings.json`: exit 1, the voice, and no state store created
  (the refusal is before any store, 3).
- `TestModelsFileRowListsAndSwitches` — a file-added row: `/models`
  lists it with its role; `models <id>` switches; the next turn's
  request carries its model (4).

The suite is green on a box with no model loaded: every case is
scripted, httptest, or a real store in a temp dir.

## the diffs this spec implies

PR A carries this spec file only; the diffs below land with PR B.

- **SPEC_CORE**: the layout gains `config/` (the leaf, the two
  embedded files). Nothing else: `core/` zero diff (10).
- **SPEC_COMPACT**: 2 — the row gains `role` / `effort`;
  `models.Defaults` leaves code for the embedded `config/models.json`;
  `Resolve`'s env overlays the active row's fields and the
  synthesized row carries its role. 3 — the summary call's effort is
  the row's, `"medium"` the field's default. The testing section gains
  the named cases.
- **SPEC_COMMANDS**: the non-goal "No config" is reversed, named; 6 —
  the `/models` line gains the role column; the table's source is the
  merged table plus the resolved active row.
- **SPEC_HARDENING**: 6's guidelines collection — the AGENTS.md
  placement is named against it (system → AGENTS.md → guidelines, 6).
- **SPEC_STATE**: one named line — the scheduler store keeps its
  `defaultModel` constant as the direct-`Create` fallback (5, 8); no
  schema change, no path change.
- **docs/SETUP.md**: the config files section (the four files, the
  locations, the precedence rule, the refusal voice, the AGENTS.md
  order and the worker semantics, `theme.json` reserved for 10); the
  env table gains the file layer; the "set empty" note extends to the
  file layer for the two presence keys.
- **CHANGELOG + Version**: `0.2.0` → `0.3.0`, the test updated (10).

## scope

What this is not:

- The TUI (10): the palette, the glass. `theme.json` is read raw and
  exposed; 10 owns its schema and its rendering (7).
- A per-cwd `settings.json` (the cwd carries `AGENTS.md` only) and a
  `/config` command (the file is the interface; the refusal teaches).
- Config reload: the files are read once at start; the `models`
  switch and the `new` handoff read the runtime table and the root's
  state, not the file.
- A config schema version (the unknown-key refusal is the version
  signal) and env expansion in file values (the file is data).
- New env vars, new flags, or a new dep: the `RIG_*` surface and the
  flag names are unchanged (8, 10); JSON only, stdlib (1).
- The loop: zero diff through this — if the TUI (10) needs a loop
  change because of config, the freeze was premature and 7 or 9 is
  reopened first, per the roadmap.

The loop at the end of this is the loop of the end of 9: L1–L8 —
byte-identical. The runtime becomes 0.3.0: config-shaped, still
frozen.
