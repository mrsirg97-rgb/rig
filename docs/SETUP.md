# rig setup

rig's core uses only the standard library. Stores use the pure-Go
`modernc.org/sqlite` driver. Setup requires a model endpoint, Go for source
builds, and bubblewrap for jailed workers.

## prerequisites

- **Go** with a toolchain that satisfies `go 1.26.6` in `go.mod`. Any Go ≥
  1.26.6 works: the toolchain line pulls the newest matching patch automatically
  (`GOTOOLCHAIN=auto` is the default). Verify with `go version`.
- An **OpenAI-compatible** chat-completions endpoint (SSE streaming): a local
  model server, a gateway, or the hosted API. rig speaks the wire protocol
  only; vendor specifics live in your endpoint configuration.
- **bubblewrap** (the `bwrap` binary) for the scheduler's jailed workers:
  the one environment dependency of that path, as git is read's
  `diff: true` (`specs/SPEC_SANDBOX.md`). Linux only; the run refuses on
  other platforms and names the profile. It is the only dependency the *scheduled worker*
  needs beyond the rig binary and the endpoint: the interactive REPL and
  one-shot runs run with or without it.

  ```sh
  # Debian / Ubuntu: the package is bubblewrap, the binary bwrap
  sudo apt-get install bubblewrap
  ```

  **Ubuntu 24.04 and friends**; the box ships
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
./rig --version        # prints the rig version
```

Choose an install path (`specs/SPEC_BUILD.md` 5):

**Installer** (POSIX sh, no Go, no sudo; installs to `~/.local/bin`):

```sh
curl -fsSL https://mrsirg97-rgb.github.io/rig/install.sh | sh
```

**Release binary** from `releases/latest`. Choose `rig_<os>_<arch>`:

```sh
curl -fsSL https://github.com/mrsirg97-rgb/rig/releases/latest/download/rig_linux_amd64 -o rig
chmod +x rig
./rig --version
```

**go install** (needs Go ≥ 1.26.6; the toolchain line pulls the newest
matching patch automatically):

```sh
go install github.com/mrsirg97-rgb/rig/v2/cmd/rig@latest
```

`rig -update` fetches, verifies, and atomically installs the latest release.
The running process keeps the old binary until restart.

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
descends, except the two presence keys (below). With no file present,
the embedded defaults alone run.

The files live in the rig home, `~/.rig/`; the same directory the
stores use (the `.pi`/`.omp` convention, not the XDG one). The home
resolves `$RIG_HOME` > `~/.rig`: the env var, when set (non-empty), is
the home; the operator's spelling, used as-is. The one-time
migration: on a start where the resolved home is absent and the old
`~/.config/rig` exists, the old directory is renamed to the resolved
home and one line says so; after that the migration is a no-op. A
present home wins, whatever the old one holds, and under an explicit
`RIG_HOME` the migration never runs: the override is isolation, not a
move order. Every file is optional; a present-but-malformed file is
a loud refusal at start naming the file and the field (exit 1, before
any store is opened), and an absent one is silent. Unknown keys refuse:
the file is a contract, not a filter. `models.json` is the one file
whose content you cannot skip: the binary ships no model rows (2.12.11),
so before your first run write the row for the model you mean to name —
`--model`, `RIG_MODEL`, or settings.json's `model` — or the start refuses
naming the row it could not find. The file stays optional in the letter
of the rule: with no rows anywhere and no model named, the refusal is the
one for the missing name.

| file              | purpose                                                                 |
|-------------------|-------------------------------------------------------------------------|
| `settings.json`   | the knobs below, flat, by their env names (lowerCamel, no `RIG_` prefix); `defaultJobModel` is retired (2.4.0): a present one is named once at start and ignored — move it to `model` by hand, then delete the key |
| `models.json`     | the model table, one row per model: `id`, `window`, `maxTokens`, `reserve`, `keepRecent` (all four numerics required on every row), optional `role` (`worker`/`interactive`, default the latter), `effort` (the summary call's reasoning effort), `efforts` (the `/effort` dial's vocabulary), `vision` (registers `view` on that row), and the hosted run site — `remote`, `provider`, `baseUrl`, `apiKey`, `reasoning`, `providerPin`, `cacheControl`, `retries` — under **"On hosted mode"** below and `specs/SPEC_HOSTED.md` |
| `workers.json`    | **retired (2.4.0)**: the fleet is the resident model. A present file is read, ignored, and named once at start (`workers.json retired: the fleet is the resident model`); deleting it silences the line. Its content is never interpreted |
| `AGENTS.md`       | global instructions; read before the project's `AGENTS.md` (the nearest one from the workspace up to the repo root) and placed between the system prompt and the participants' guidelines |
| `theme.json`      | the terminal frontend's custom theme (`specs/SPEC_TUI.md` 7), the `/theme custom` preset: `base` (one of `warm`, `cool`, `paper`, `p1`, `p3`, or the legacy `oled`; required), optional `slots` (the slot names → `#rrggbb`) and `glyphs` (`unicode` or `ascii`). Unknown keys refuse; the TUI owns the schema. The preset dial itself is settings.json's `theme` key (`/theme warm|cool|custom`) |

The project's `AGENTS.md` is the nearest one walking up from the
workspace to the repository root (the directory holding `.git`) and no
further, so a session opened in a subdirectory reads the repo's contract
and a workspace that is no repository reads only its own file. It is read
when the session wires, so a session that opens in another workspace
carries that workspace's contract; a scheduled worker inherits its job's
cwd's file, not the creating session's.

| knob          | flag           | env                    | file key        | embedded default |
|---------------|----------------|------------------------|-----------------|------------------|
| endpoint      | `--base-url`   | `RIG_BASE_URL`         | `baseUrl`       | `http://127.0.0.1:8090/v1` (the model server; a jailed worker's proxy forwards here) |
| model         | `--model`      | `RIG_MODEL`            | `model`         | none (a run without one refuses at start, naming the three ways) |
| system        | `--system`     | `RIG_SYSTEM`           | `system`        | rig's default system prompt |
| allow-list    | `--allow` (CSV)| `RIG_ALLOW` (CSV)      | `allow` (JSON array) | the embedded default: twelve names, `scheduler` and `delegate` appended unless the file carries its own `allow` key (then you decide what is permitted); the menu's arithmetic is `docs/USAGE.md`'s |
| bound         | `--retries`    | `RIG_RETRIES`          | `retries`       | `3` |
| round cap     |                | `RIG_ROUNDS` (invalid loudly refuses) | `rounds` | `0` = no cap (the default); `N` caps the turn's tool calls (SPEC_HARDENING 9) |
| result cap    |                | `RIG_RESULT_CAP` (invalid loudly refuses) | `resultCap` | `65536` (64 KiB); the wall on every tool result |
| resume        | `--resume <id>`|  |  | fresh session (refuses with `-p`; one-shot stays one-shot) |
| terminal      | `--tui` (auto/true/false) |  |  | `auto`: the terminal frontend when stdout is a terminal, the piped CLI otherwise (one-shot `-p` is never a TUI) |
| python kernel |                | `RIG_PYTHON`           | `python`        | the default interpreter |
| web search    |                | `RIG_SEARXNG_URL`      | `searxngUrl`    | `http://127.0.0.1:8888` (the web-tools compose) |
| web fetch     |                | `RIG_WEB_FETCH_PROXY`  | `webFetchProxy` | `http://127.0.0.1:8889`; **presence key**: set empty = direct |
| extraction    |                | `RIG_TRAFILATURA`      | `trafilatura`   | none (auto); **presence key**: set empty = the stdlib text pass |
| session model |                | `RIG_MODEL`            | `model`          | no embedded default; the worker model resolves at claim time: the named one, else the resident model, else this |
| swap endpoint |                | `RIG_SWAP_URL`         | `swapUrl`         | `http://127.0.0.1:8090`; the jailed worker's socket proxy forwards to it |
| decision server |  | `RIG_DECISION_URL`     | `decisionUrl`     | none; set it and every bash call gets a pending risk proposal the reviewer settles, and the `decide` tool joins the live table so the model hands it the sorting (SPEC_DECISION); unset, nothing proposes and the menu, the wire sha and the system prompt do not move |
| decision unit |  |  | `decisionUnit`    | none; the systemd user unit file a promotion rewrites — the one `Environment=` line naming `RIG_DECISION_CHECKPOINT` (SPEC_DECISION 2.13.0); unset, runs score and record but promote nothing |
| trainer interpreter |  |  | `trainPython`     | none; the `train/` zone's own interpreter, run headless with torch — two gigabytes that do not belong in the session kernel's venv (SPEC_DECISION 2.13.0); a run without it refuses naming the key |
| approval dial  |                |  | `approve`         | `auto`; `manual` pauses every mutating tool call for the operator's y/n |
| worker sandbox |              |                      | `sandbox`         | `jailed` (bwrap, unshare-all); `landlock` (the in-kernel jail, `RIG_LANDLOCK` and `RIG_EXEC_WRAPPER`); `off` = unjailed (one loud line per worker run, the operator's explicit act) |
| sandbox binds |  |  | `sandboxBinds` (JSON array) | none; an entry is an absolute path, ro-bound unless it ends `:rw` |
| update key    |                | `RIG_UPDATE_KEY`      | `updateKey`         | the embedded pinned key that signs releases (SPEC_BUILD 5); env and file override it; a build without a pinned key refuses `-update` |
| review batch  |                |                      | `reviewBatch`     | `3`: settled-review rows per review fire (SPEC_DECISION); `0` leaves the reviewer off; negative or non-integer refuses |
| plugin cap    |                |                      | `plugins` (object) | no cap; `plugins.max` caps the live plugin set and an over-cap load is skipped naming the cap — the number is read at startup, so raising it takes a restart |
| worker pair   |                |                      | `workers`         | on; `false` turns `delegate` and the swarm off |
| model row     |                | `RIG_MODEL_WINDOW` (+ `_MAX_TOKENS`, `_RESERVE`, `_KEEP_RECENT`, `_RETRIES`; and `_BASE_URL`, `_API_KEY`, `_REASONING`, `_PROVIDER`, `_REMOTE`); `_CONCURRENCY` does not exist: it is ignored with no line at all | `models.json` | none: the table is the operator's file (`RIG_MODEL_WINDOW` alone still mints a row for the active id) |

**On the worker sandbox**: `sandbox` is the scheduled worker's jail
(`specs/SPEC_SANDBOX.md` 1, 5): `jailed` (the default; fail closed)
spawns the worker under bwrap's unshare-all profile, netless except
the one bound socket its model calls ride, with its home a scratch
directory inside the job's cwd; `off` runs the worker as before and
names that fact once per worker run. The refusal is loud and recorded:
bwrap absent refuses with both settings keys named. The interactive
REPL and one-shot runs never consult the sandbox code. `sandboxBinds`
rides the profile as extra binds (the operator's need, e.g. a python
venv): absolute paths, read-only by default, `:rw` opts one in. The
profile is the spec's block, verbatim (`store/scheduler/jail.go`).

**On the presence keys**; `RIG_WEB_FETCH_PROXY` and `RIG_TRAFILATURA`
are presence-aware at every layer: "set empty" means present but
empty — an explicit choice (direct egress / the stdlib text pass) —
while an unset value descends. Presence is the signal, the value is
the choice.

**On hosted mode** (`specs/SPEC_HOSTED.md`); a row running on a remote
endpoint says where: `remote: true` or `provider: "openrouter"` (a name
implies remote), plus `baseUrl`, `apiKey` (the bearer key, from the
file or `RIG_MODEL_API_KEY`, never logged or rendered), `reasoning`
(OpenRouter rows read and echo `reasoning` / `reasoning_details`; the
default stays `reasoning_content`), and the openrouter-only
`providerPin` and `cacheControl`. Remote rows omit the
llama-server-only fields and skip the local swap entirely; 429 and 5xx
retry with bounded backoff (`retries`, default 3) instead of faulting
the turn. Cost rides the endpoint's `usage.cost` into the state
store's cost column, shows in the TUI footer, and sums into a swarm's
`budget=` and a job's `budget`.

**On the model row**; compaction is per-model: the active model must
resolve to a row (window, max tokens, reserve, keep-recent). The table
**is your `models.json`**: the binary ships no model rows,
so a row you do not write does not exist, and naming a model with no row
refuses at start — before any store opens or request is made — naming the
missing id and the ids the table does know. The merge keeps its shape
(fields you set replace the row beneath, unset ones keep it, a new id
joins with its numeric fields required), it simply has nothing beneath
to overlay. `RIG_MODEL_*` overlays the active id's fields and
synthesizes a row for an unknown id (the loud refusal otherwise).
`/models` lists the runtime table — what you wrote, and nothing else —
and switches the active model.

**On the `models.json` zero edge**; zero means unset at the overlay
layer: a row written whole can carry a zero numeric, an overlay cannot.

**On `RIG_RETRIES`**; read before tuning: the value does **not** permit
silent re-execution. Every tool call executes exactly once; the value
bounds the *model's* re-issuance of a failing *tool* — identical retries
only, a corrected call always executing, the streak cleared at the start
of every turn. The full rule is `docs/USAGE.md`'s, its one home.

**On `--resume`**; it rebuilds the session from the state store in one
read-only transaction — the semantics are `docs/USAGE.md`'s. The
per-process state (the guard's counts, the steering slot) starts
fresh.

**On the allow-list**; it is default-deny below the list, and denials
are named refusals fed back to the model. The list, the plugin door's
second admission path, and how to narrow it are `docs/USAGE.md`'s, its
one home. Two setup facts stay here: the embedded default permits the
built-in set because a default-deny CLI would ship a dead agent, and a
`settings.json` that writes its own `allow` key replaces that default
whole — it must carry `plugin`, or every door call is refused.

## example configuration

Three files, written into the rig home (`~/.rig/`, or `$RIG_HOME` when
set). Every file is optional, so these are the ones to copy when a blank
home is not what you want; an omitted key keeps its embedded default,
and an unknown key refuses at start naming the file and the field.

**`settings.json`**: the knobs, flat, by their env names:

```json
{
  "baseUrl": "http://127.0.0.1:8090/v1",
  "model": "local",
  "allow": ["bash", "read", "write", "edit", "view", "python", "web", "decide", "todo", "rem", "sessions", "plugin"],
  "retries": 3,
  "resultCap": 65536,
  "approve": "auto",
  "sandbox": "jailed",
  "sandboxBinds": [],
  "workers": true
}
```

`baseUrl` and `model` are the two a run needs; the rest are the
embedded defaults written out. `workers` is the fleet's switch:
`false` keeps `delegate` and the swarm off a capable machine; `true`
(or absent) still wires the pair only where a second request can
actually run — the swap readable at start, or a remote row
(SPEC_WORKERS). `allow` is the one
to be careful with:
an `allow` you write replaces the default whole (default-deny below
it), so it must carry `plugin` or every door call is
refused, and it must carry `scheduler` and `delegate` when you also
configure a fleet. `retries` bounds the model's re-issuance of a
failing tool call; it is not a retry allowance. `approve: "manual"`
pauses every mutating call for your y/n; `sandbox: "off"` is the
operator's explicit unjailing of the scheduled worker, one loud line
per run.

**`models.json`**: the per-model table — this file is the table (the
embedded one is empty), merged by id over whatever the build ships:

```json
[
  {"id": "local", "window": 65536, "maxTokens": 8192, "reserve": 8192, "keepRecent": 16384, "role": "interactive", "efforts": ["low", "medium", "xhigh"]},
  {"id": "worker", "window": 32768, "maxTokens": 4096, "reserve": 4096, "keepRecent": 8192, "role": "worker", "efforts": ["low", "medium"]},
  {"id": "openrouter-sonnet", "window": 200000, "maxTokens": 8192, "reserve": 16384, "keepRecent": 40000, "remote": true, "provider": "openrouter", "baseUrl": "https://openrouter.ai/api/v1", "apiKey": "sk-or-...", "reasoning": "reasoning", "providerPin": "Together", "cacheControl": true}
]
```

`id` must match the model id you pass to `--model`. Every row carries
its numeric fields: with nothing embedded, an omitted `window`,
`maxTokens`, `reserve`, or `keepRecent` refuses the row by name at
start. `role` is `interactive` (the default) or `worker`; `efforts` is
the `/effort` dial's vocabulary. The third row is a hosted run site
(`specs/SPEC_HOSTED.md`): `remote` and `provider` (a name implies
remote), `baseUrl` (the endpoint), `apiKey` (the bearer key, from the
file or `RIG_MODEL_API_KEY`), `reasoning` (`reasoning` for OpenRouter, the default
`reasoning_content` otherwise), `providerPin` and `cacheControl`
(openrouter-only), `retries` (the 429/5xx retry bound, default 3 for
remote rows).

**The workers** (`specs/SPEC_WORKERS.md`): the fleet is the resident
model. A worker's model resolves at claim time — the named one, else
the resident model (what the swap has loaded), else the session's
default — and a model that is not resident refuses, naming the holder;
nothing evicts from inside a turn. There is no slot gate: the resident
server queues requests, so every spawn site — a delegate, a scheduler
fire, the swarm's worker — sends and waits. A delegate has no clock: its
worker lives until it exits or the session ends; a scheduled fire
carries its own timeout, and a swarm's `budget=` and a job's `budget`
bound the money. `delegate` and the swarm are wired wherever a second
request can run — the session's model row is remote, or the swap answers
one live read (one slot hosts the pair; a queued request waits at the
server); `workers: false` turns them off; the scheduler is wired
everywhere; the menu says nothing about what is absent, and `/swarm`
names the reason when it refuses. A
`workers.json` left in the rig home is named once at start and
ignored; delete it to silence the line.

## plugins

Python plugins as tools (`specs/SPEC_PLUGINS.md`): one file under
`~/.rig/plugins/` is one tool, discovered at startup, reached on the
wire through the one `plugin` door, pending until the operator
approves. The contract, the zones, the `/plugins` verbs, the
provenance rule, and the train zone: `docs/PLUGINS.md`, its one home.
Setup's own share: the zone lives in the rig home; a `settings.json`
that writes its own `allow` key must carry `plugin` or every door call
is refused; `plugins.max` caps the live set (read at startup, so
raising it takes a restart).

## dashboard

`rig serve` is rig with the page as its terminal
(`specs/SPEC_SERVE.md`): the live session streamed in the TUI's
grammar — approvals answered in place, stop while a turn runs, and the
same commands typed into the same `❯` prompt — beside the stores with
their writes: sessions per workspace with transcripts and resume; the
queue (create, start, complete, retry); the jobs with their run audit
(create, pause, resume, remove, repair); the swarm, live, with start
and stop; the model table, switch, and effort dial; and the plugins'
three zones with the forge's source read and save into the pending
zone. Loopback bind only (a non-loopback address is refused by name),
behind a token minted and printed once, stored 0600 and exchanged for
a cookie. A sidebar on desktop, a tab bar on the phone, `warm` and
`cool` palettes; from Safari, share → add to home screen installs it
as an app. There is no memory view: rem is the model's, read in the TUI.

## terminal

The default REPL is the terminal frontend (`specs/SPEC_TUI.md`): the
same session, the same commands, the same exits; themed, with the
three-row status; identity (`model · used/window`), the stance
(`effort · role · auto|manual`), and the usage totals; the live region (the
activity row and the input line, redrawn in place), the todo and
scheduler blocks, and the usage line at every turn's end. The piped CLI
is unchanged and is the reference: pipe, `-p`, and `--tui=false` all
speak the CLI's bytes.

- `--tui auto` (the default) picks by the terminal: stdout a terminal
  → the TUI; piped or redirected → the CLI. `--tui=true` forces the
  TUI (a pty, `tmux capture-pane`); `--tui=false` forces the CLI.
- **Theme**: `/theme warm|cool|custom` in the REPL (2.3.2), persistent
  as settings.json's `theme` key; the TUI repaints at once. `custom`
  is `~/.rig/theme.json`, three keys: `base` (the shipped palette:
  `warm`, `cool`, `paper`, `p1`, `p3`; `oled` is the legacy alias of
  `warm`), `slots` (any of the slot names; `accent`, `dim`, `error`,
  `reasoning`, `rule`, `success`, `text`, `warn`; mapped to a
  `#rrggbb` color), `glyphs` (`unicode`, the default, or `ascii` for
  the bracket/`>`/`#` set). Color depth is the terminal's, not yours:
  `COLORTERM` 24-bit → truecolor, else the nearest 256 index. A
  malformed file refuses at start, naming the file and the key;
  `custom` without the file refuses by name.
- **Input**: the line's arrows and Home/End move the cursor;
  Backspace and Delete cross a wide glyph whole; Up/Down walk the
  session's history (in memory, the draft preserved around a trip).
  **Ctrl-T** toggles the rendering of reasoning for the rest of the
  session (committed history is untouched). A line typed while a turn
  is live steers (it interrupts the turn and delivers at the next
  prompt, latest wins); pasted lines are separate prompts, in order.
  **Ctrl-C** ends the session (interrupting a live turn first);
  **Ctrl-D** exits at the empty prompt (a non-blank line is kept).
  **Esc** walks a ladder, outermost first: a pager open closes it, a
  menu open closes it (the input keeps its text), and on an empty
  prompt it interrupts a live turn — or, with none live, stops every
  running delegated worker and lets their returns name the interrupt
  as their exit; where no delegate is wired it stays the prompt clear.
  A `/` line is a command (`/models`, `/new`, `/todo` …); `//` escapes
  the slash into a prompt. Raw mode is on while the session runs and
  restored at exit; a resize repaints the live region at the terminal's
  new width (history is the terminal's, as usual).

## verify

```sh
./rig --version                 # prints the rig version
./rig --base-url $YOUR_ENDPOINT --model $NAME --system "be terse"
```

then type a prompt. A line typed while a turn is live steers (it interrupts
the turn and is delivered at the next prompt; latest wins); Ctrl-C ends the
session once the in-flight step unwinds; Ctrl-D exits at the prompt. If
nothing streams, check the endpoint first; rig surfaces provider faults
verbatim and loudly; it does not hide them.

The terminal's verify: the three-row status (identity, stance, usage),
a streaming turn with its activity row, a tool line with its glyph and
duration, the usage line at the turn's end, and a clean Ctrl-D exit.
`--tui=true` under `tmux` gives the same session to `capture-pane`
(piped `--tui=false` stays the byte reference).

## training

The decision model trains from rig's own rows (`specs/SPEC_DECISION.md`
2.13.0). The trainer zone and its contract, and the separate
interpreter it runs through, are `docs/PLUGINS.md`'s train-zone section;
the run, the scoring, and the promotion rule are the spec's, and the
pipeline as architecture is `docs/DESIGN.md`'s. Setup's own share:
`trainPython` must name an interpreter with `IPython` importable (the
trainer venv wants one `pip install ipython`), and `decisionUnit` names
the systemd unit a promotion rewrites its one `Environment=` line in —
without them nothing trains and nothing promotes.
`/decision train <trainer>` lands the run on the scheduler; it never
runs inside a turn.
