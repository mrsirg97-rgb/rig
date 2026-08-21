# rig setup

rig's core is **stdlib-only**: `core/`, `loop/`, the provider, the tools,
and the frontends carry no dependencies. The one leaf dependency is
`modernc.org/sqlite` (pure-Go driver) for the stores, justified in
`specs/SPEC_STATE.md`. Setup is fetch, configure, build.

## prerequisites

- **Go** with a toolchain that satisfies `go 1.26.6` in `go.mod`. Any Go ≥
  1.26 works: the toolchain line pulls the newest matching patch automatically
  (`GOTOOLCHAIN=auto` is the default). Verify with `go version`.
- An **OpenAI-compatible** chat-completions endpoint (SSE streaming): a local
  model server, a gateway, or the hosted API. rig speaks the wire protocol
  only; vendor specifics live in your endpoint configuration.
- **bubblewrap** (the `bwrap` binary) for the scheduler's jailed workers —
  the one environment dependency of that path, as git is the diff tool's
  (`specs/SPEC_SANDBOX.md`). Linux only; the run refuses on other platforms
  and names the profile. It is the only dependency the *scheduled worker*
  needs beyond the rig binary and the endpoint: the interactive REPL and
  one-shot runs run with or without it.

  ```sh
  # Debian / Ubuntu: the package is bubblewrap, the binary bwrap
  sudo apt-get install bubblewrap
  ```

  **Ubuntu 24.04 and friends** — the box ships
  `kernel.apparmor_restrict_unprivileged_userns=1`, and AppArmor then
  blocks the unprivileged user namespace's netns setup: bwrap fails with
  `loopback: Failed RTM_NEWADDR: Operation not permitted`. The box's
  choice, named by the refusal:

  ```sh
  sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0
  ```

## build

```sh
git clone git@github.com:mrsirg97-rgb/rig.git
cd rig
go build ./cmd/rig     # produces ./rig
./rig --version        # rig 0.8.0
```

The install paths, three (`specs/SPEC_BUILD.md` 5):

**Installer** (POSIX sh, no Go, no sudo — lands in `~/.local/bin`):

```sh
curl -fsSL https://mrsirg97-rgb.github.io/rig/install.sh | sh
```

**Release binary** — the same asset the installer fetches, downloaded
directly (no script): pick `rig_<os>_<arch>` from `releases/latest`;
the version lives in the URL, not the name.

```sh
curl -fsSL https://github.com/mrsirg97-rgb/rig/releases/latest/download/rig_linux_amd64 -o rig
chmod +x rig
./rig --version
```

**go install** — pre-tag `@master` works today, `@latest` once tagged;
the one prerequisite is Go ≥ 1.26 (the toolchain line pulls the newest
matching patch automatically):

```sh
go install github.com/mrsirg97-rgb/rig/cmd/rig@master   # today (no tag yet)
go install github.com/mrsirg97-rgb/rig/cmd/rig@latest   # once tagged
```

`make install` is the same build landed locally: `$(go env GOBIN)` when
set, else `~/.local/bin`; `BINDIR=...` names the directory.

Contributors: the gate before any change is

```sh
go test ./... -count=1
go vet ./...
gofmt -l .
```

## configure

Every knob is a four-layer resolution, per key:
**flag > env > file > embedded default** (`specs/SPEC_CONFIG.md`). A key
set at any layer beats the layers below; an unset layer descends. A flag
you typed always wins, whatever its value; an empty env or file value
descends, except the two presence keys (below). No file present is the
0.2.0 behavior, exactly — the embedded layer is the 0.2.0 values moved
out of code.

The files live in the rig home, `~/.rig/` — the same directory the
stores use (the `.pi`/`.omp` convention, not the XDG one). The home
resolves `$RIG_HOME` > `~/.rig`: the env var, when set (non-empty), is
the home — the operator's spelling, used as-is. The one-time
migration: on a start where the resolved home is absent and the old
`~/.config/rig` exists, the old directory is renamed to the resolved
home and one line says so; after that the old directory is gone and
the migration is a no-op. A present home wins, whatever the old
directory holds. The migration never runs under an explicit
`RIG_HOME` — the override is isolation, not a move order: an absent
override stays absent, and the old home stays put. Every file is optional; a present-but-malformed file is
a loud refusal at start naming the file and the field (exit 1, before
any store is opened), and an absent one is silent. Unknown keys refuse:
the file is a contract, not a filter.

| file              | purpose                                                                 |
|-------------------|-------------------------------------------------------------------------|
| `settings.json`   | the knobs below, flat, by their env names (lowerCamel, no `RIG_` prefix) |
| `models.json`     | the model table: rows of `id`, `window`, `maxTokens`, `reserve`, `keepRecent`, optional `role` (`worker`/`interactive`, default `interactive`), `effort` (the compaction summary call's reasoning effort, default the policy's `medium`) and `efforts` (the model's available effort levels — `low`, `medium`, `xhigh` — the `/effort` dial's vocabulary) |
| `AGENTS.md`       | global instructions; read before `<cwd>/AGENTS.md` (project) and placed between the system prompt and the participants' guidelines |
| `theme.json`      | the terminal frontend's theme (`specs/SPEC_TUI.md` 7): `base` (one of `oled`, `paper`, `p1`, `p3`, required), optional `slots` (the eight slot names → `#rrggbb`) and `glyphs` (`unicode` or `ascii`). Unknown keys refuse; the TUI owns the schema |

`<cwd>/AGENTS.md` is read from the working directory: the REPL's cwd, or,
for a scheduled worker, the job's cwd — the job inherits its own working
directory's project file, not the creating session's.

| knob          | flag           | env                    | file key        | embedded default |
|---------------|----------------|------------------------|-----------------|------------------|
| endpoint      | `--base-url`   | `RIG_BASE_URL`         | `baseUrl`       | `http://127.0.0.1:8090/v1` (the worker swap) |
| model         | `--model`      | `RIG_MODEL`            | `model`         | `local` |
| system        | `--system`     | `RIG_SYSTEM`           | `system`        | rig's default system prompt |
| allow-list    | `--allow` (CSV)| `RIG_ALLOW` (CSV)      | `allow` (JSON array) | the 15 built-in tools |
| bound         | `--retries`    | `RIG_RETRIES`          | `retries`       | `3` |
| resume        | `--resume <id>`| —                      | —               | fresh session (refuses with `-p`; one-shot stays one-shot) |
| terminal      | `--tui` (auto/true/false) | — | — | `auto`: the terminal frontend when stdout is a terminal, the piped CLI otherwise (one-shot `-p` is never a TUI) |
| python kernel |                | `RIG_PYTHON`           | `python`        | the default interpreter |
| web search    |                | `RIG_SEARXNG_URL`      | `searxngUrl`    | `http://127.0.0.1:8888` (the web-tools compose) |
| web fetch     |                | `RIG_WEB_FETCH_PROXY`  | `webFetchProxy` | `http://127.0.0.1:8889`; **presence key**: set empty = direct |
| extraction    |                | `RIG_TRAFILATURA`      | `trafilatura`   | none (auto); **presence key**: set empty = the stdlib text pass |
| scheduler job |                | —                      | `defaultJobModel` | `qwen3.8-workers`; a job's explicit `model` arg beats it |
| swap endpoint |                | `RIG_SWAP_URL`         | `swapUrl`         | `http://127.0.0.1:8090`; the jailed worker's socket proxy forwards to it |
| approval dial  |                | —                      | `approve`         | `auto`; `manual` pauses every mutating tool call for the operator's y/n |
| worker sandbox | —              | —                      | `sandbox`         | `jailed`; `off` = unjailed (one loud line per worker run, the operator's explicit act) |
| sandbox binds | —              | —                      | `sandboxBinds` (JSON array) | none; an entry is an absolute path, ro-bound unless it ends `:rw` |
| model row     |                | `RIG_MODEL_WINDOW` (+ `_MAX_TOKENS`, `_RESERVE`, `_KEEP_RECENT`) | `models.json` | the two-row table |

**On the worker sandbox** — `sandbox` is the scheduled worker's jail
(`specs/SPEC_SANDBOX.md` 1, 5): `jailed` (the default — fail closed)
spawns the worker under bwrap's unshare-all profile, netless except
the one bound socket its model calls ride, with its home a scratch
directory inside the job's cwd; `off` runs the worker as before and
names that fact once per worker run. The refusal is loud and recorded:
bwrap absent refuses with both settings keys named. The interactive
REPL and one-shot runs never consult the sandbox code. `sandboxBinds`
rides the profile as extra binds (the operator's need, e.g. a python
venv): absolute paths, read-only by default, `:rw` opts one in. The
profile is the spec's block, verbatim (`store/scheduler/jail.go`).

**On the presence keys** — `RIG_WEB_FETCH_PROXY` and `RIG_TRAFILATURA`
are presence-aware at every layer: "set empty" means present but empty,
an explicit choice (direct egress / the stdlib text pass), while an
unset value descends to the next layer. Presence is the signal, the
value is the choice.

**On the model row** — compaction is per-model: the active model must
resolve to a row (window, max tokens, reserve, keep-recent). The table
is the embedded rows overlaid by `models.json`, merged by id: fields you
set replace the embedded row's, fields you leave unset keep it, a new id
is added (numeric fields required), and a row you do not list stays.
`RIG_MODEL_*` overlays the active id's fields, set beats the row, and
synthesizes a row for an id the table does not know (a loud refusal
naming the known ids otherwise). `/models` lists the runtime table with
its role column and switches the active model.

**On the `models.json` zero edge** — zero means unset at the overlay
layer, so a zero numeric field on an embedded id is unreachable (a new
row can carry it, an embedded id cannot): the named cost of the rule.

**On `RIG_RETRIES`** — read before tuning: the value does **not** permit
silent re-execution. Every tool call executes exactly once; the value bounds
the *model's* re-issuance of a failing *tool* — keyed by tool name, with
the streak per args: the bound strikes identical retries only, and a
corrected call (args differing from the last failed args) resets its own
streak and always executes; the bound is cleared at the start of every turn.
The limit-th consecutive failure of a call carries a note telling the model
to read the error and change the call or stop calling the tool; the next
re-issuance of that call is refused without executing, naming the
bound. A successful call clears the count: the bound tracks streaks within
a turn, not history. It is a brake on repetition, not a retry allowance.

**On `--resume`** — it rebuilds the session from the state store (the
transcript in order, assistant reasoning, the tool calls, the file
provenance, the identity) in one read-only transaction; dangling tool calls
are kept, an unknown id is loud, and the recorder adopts the existing row so
one identity serves todo's claims and rem's sources. The per-process state
starts fresh: the guard's counts and the steering slot are not persisted.

**On the allow-list** — it is default-deny below it: any tool not named is
refused at the boundary and the refusal is fed back to the model. The default
permits the 15 built-in tools because a default-deny CLI would ship a
dead agent; narrow with `--allow read` or similar. Python plugins
(`~/.rig/plugins/`) are **not** in the default — but a plugin sitting in
`plugins/` root is itself an allow-list entry (SPEC_PLUGINS 7): the
provenance rule forces a model's writes into `plugins/pending/`, so an
installed plugin got there by the operator's `/plugins` approve, and that
presence admits it through the allow-list's second door (the live plugin
table). A plugin still in `plugins/pending/` is not live and stays
refused until approved and reloaded; the refusal's voice names the tool
and the allow-list either way.

## plugins

Python plugins as tools (`specs/SPEC_PLUGINS.md`): one file, one tool.

- **The directory** — `~/.rig/plugins/` (the rig home's, top-level
  `*.py` only, in filename order). No directory, or an empty one, is a
  no-op that never starts the kernel; with no plugins the wire is the
  built-in tools' bytes exactly. `plugins/pending/` is the forge's
  landing zone (the model's authoring): invisible to discovery by the
  top-level rule, and a fact of the home — created at startup, silent
  and idempotent.
- **The file's contract** — three names:

  ```python
  DESCRIPTION = "what the tool does, for the model"
  SCHEMA = {"type": "object", "properties": {"text": {"type": "string"}}}

  def run(args: dict) -> str:
      return "echo: " + args["text"]
  ```

  The tool's name is the filename stem (`echo.py` → `echo`); the
  description and schema ride the wire verbatim.
- **Discovery at startup** — the files are imported through the shared
  python kernel (the same persistent kernel as the `python` tool: the
  namespace is shared on purpose, so the model's python can call plugin
  functions directly, and plugin state persists across calls). A file
  missing a piece or failing import is a loud skip (one line naming the
  file and the field; startup continues); a name colliding with a
  built-in tool refuses the start loud (native-wins would be silent
  shadowing).
- **The call** — the kernel invokes the module's `run` with the model's
  args dict; the return value is the tool result; an exception is a
  tool error carrying the traceback tail, and the kernel stays alive
  (it is the model's kernel too).
- **The provenance rule** (SPEC_SANDBOX 2) — the model's `write` and
  `edit` refuse a target inside `plugins/` that is not inside
  `plugins/pending/`; the refusal teaches the shape: `permission
  denied: <path> is in plugins/ outside plugins/pending/ (plugins
  install by the operator's /plugins approve; write to
  plugins/pending/)`. The rule is the guard for the honest path, not
  the boundary: bash can still move a file into `plugins/` (the
  operator's shell is the operator's); the worker jail (SPEC_SANDBOX
  1) is the boundary, the provenance rule is the workflow.
- **The allow-list** — a plugin is not in the built-in default, but an
  installed plugin's presence in `plugins/` root is itself its allow-list
  entry (SPEC_PLUGINS 7): the operator's approve put it there, and the
  allow-list's second door (the live plugin table) admits it without an
  `allow` line.
- **`/plugins`** — the loaded plugins (name, description, file) and the
  skipped ones with their reasons. `pending` lists the pending zone
  with each file's DESCRIPTION (read without running the file);
  `approve <name>` moves one to the top level — the operator's verb,
  never a tool call; a name that collides with a built-in tool refuses
  with the startup collision's voice, and an already-installed file of
  the name refuses too.
- **`plugins_reload`** — the fifteenth native tool (`specs/SPEC_PLUGINS.md`
  8): re-runs the discovery over `plugins/` and swaps the kernel's tool
  list, so a plugin registers without a restart; removal is free (the
  list rebuilds from disk). `/plugins reload` is the operator's same verb
  from the command door; `/plugins create <text>` queues the authoring
  prompt (the steer precedent: the command queues a line, never dispatches
  a turn), the model's `write` lands the file in `plugins/pending/`, and
  `approve` installs it. The `plugin` door self-heals (SPEC_STREAMLINE
  4): an unknown name re-discovers once before refusing, so an
  out-of-band install is callable without a reload call; `plugins_reload`
  stays the operator's explicit verb.
- **The sandbox** — the provenance rule is the workflow (SPEC_SANDBOX
  2); the worker jail is the boundary (SPEC_SANDBOX 1, 3, 5): a scheduled
  worker's plugins run jailed under bwrap. In the interactive REPL the
  plugins run with rig's privileges, in the operator's kernel — trust
  them as you trust your own python.

## terminal

The default REPL is the terminal frontend (`specs/SPEC_TUI.md`): the
same session, the same commands, the same exits — themed, with the
three-row status — identity (`model · used/window`), the stance
(`effort · role · auto|manual`), and the usage totals — the live region (the
activity row and the input line, redrawn in place), the todo and
scheduler blocks, and the usage line at every turn's end. The piped CLI
is unchanged and is the reference: pipe, `-p`, and `--tui=false` all
speak the CLI's bytes.

- `--tui auto` (the default) picks by the terminal: stdout a terminal
  → the TUI; piped or redirected → the CLI. `--tui=true` forces the
  TUI (a pty, `tmux capture-pane`); `--tui=false` forces the CLI.
- **Theme** — `~/.rig/theme.json`, three keys: `base` (the
  shipped palette: `oled`, `paper`, `p1`, `p3`), `slots` (any of the
  eight — `accent`, `dim`, `error`, `reasoning`, `rule`, `success`,
  `text`, `warn` — mapped to a `#rrggbb` color), `glyphs` (`unicode`,
  the default, or `ascii` for the bracket/`>`/`#` set). Color depth is
  the terminal's, not yours: `COLORTERM` 24-bit → truecolor, else the
  nearest 256 index. A malformed file refuses at start, naming the
  file and the key.
- **Input** — the line's arrows and Home/End move the cursor;
  Backspace and Delete cross a wide glyph whole; Up/Down walk the
  session's history (in memory, the draft preserved around a trip).
  **Ctrl-T** toggles the rendering of reasoning for the rest of the
  session (committed history is untouched). A line typed while a turn
  is live steers (it interrupts the turn and delivers at the next
  prompt, latest wins); pasted lines are separate prompts, in order.
  **Ctrl-C** ends the session (interrupting a live turn first);
  **Ctrl-D** exits at the empty prompt (a non-blank line is kept).
  A `/` line is a command (`/models`, `/new`, `/todo` …); `//` escapes
  the slash into a prompt. Raw mode is on while the session runs and
  restored at exit; a resize repaints the live region at the terminal's
  new width (history is the terminal's, as usual).

## verify

```sh
./rig --version                 # prints: rig 0.8.0
./rig --base-url $YOUR_ENDPOINT --model $NAME --system "be terse"
```

then type a prompt. A line typed while a turn is live steers (it interrupts
the turn and is delivered at the next prompt; latest wins); Ctrl-C ends the
session once the in-flight step unwinds; Ctrl-D exits at the prompt. If
nothing streams, check the endpoint first — rig surfaces provider faults
verbatim and loudly; it does not hide them.

The terminal's verify: the three-row status (identity, stance, usage),
a streaming turn with its activity row, a tool line with its glyph and
duration, the usage line at the turn's end, and a clean Ctrl-D exit.
`--tui=true` under `tmux` gives the same session to `capture-pane`
(piped `--tui=false` stays the byte reference).
