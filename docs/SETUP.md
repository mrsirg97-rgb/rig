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

## build

```sh
git clone git@github.com:mrsirg97-rgb/rig.git
cd rig
go build ./cmd/rig     # produces ./rig
./rig --version        # rig 0.3.0
```

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

The files live in the config home, `~/.config/rig/` — the same directory
the stores use. Every file is optional; a present-but-malformed file is
a loud refusal at start naming the file and the field (exit 1, before
any store is opened), and an absent one is silent. Unknown keys refuse:
the file is a contract, not a filter.

| file              | purpose                                                                 |
|-------------------|-------------------------------------------------------------------------|
| `settings.json`   | the knobs below, flat, by their env names (lowerCamel, no `RIG_` prefix) |
| `models.json`     | the model table: rows of `id`, `window`, `maxTokens`, `reserve`, `keepRecent`, optional `role` (`worker`/`interactive`, default `interactive`) and `effort` (the compaction summary call's reasoning effort, default the policy's `medium`) |
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
| allow-list    | `--allow` (CSV)| `RIG_ALLOW` (CSV)      | `allow` (JSON array) | the 13 built-in tools |
| bound         | `--retries`    | `RIG_RETRIES`          | `retries`       | `3` |
| resume        | `--resume <id>`| —                      | —               | fresh session (refuses with `-p`; one-shot stays one-shot) |
| terminal      | `--tui` (auto/true/false) | — | — | `auto`: the terminal frontend when stdout is a terminal, the piped CLI otherwise (one-shot `-p` is never a TUI) |
| python kernel |                | `RIG_PYTHON`           | `python`        | the default interpreter |
| web search    |                | `RIG_SEARXNG_URL`      | `searxngUrl`    | `http://127.0.0.1:8888` (the web-tools compose) |
| web fetch     |                | `RIG_WEB_FETCH_PROXY`  | `webFetchProxy` | `http://127.0.0.1:8889`; **presence key**: set empty = direct |
| extraction    |                | `RIG_TRAFILATURA`      | `trafilatura`   | none (auto); **presence key**: set empty = the stdlib text pass |
| scheduler job |                | —                      | `defaultJobModel` | `qwen3.8-workers`; a job's explicit `model` arg beats it |
| model row     |                | `RIG_MODEL_WINDOW` (+ `_MAX_TOKENS`, `_RESERVE`, `_KEEP_RECENT`) | `models.json` | the two-row table |

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
the *model's* re-issuance of a failing *tool* — keyed by tool name, so
drifting arguments do not dodge it, and cleared at the start of every turn.
The limit-th consecutive failure of a tool carries a note telling the model
to read the error and change the call or stop calling the tool; the next
re-issuance is refused without executing, naming the bound. A successful call
clears the count: the bound tracks streaks within a turn, not history. It is
a brake on repetition, not a retry allowance.

**On `--resume`** — it rebuilds the session from the state store (the
transcript in order, assistant reasoning, the tool calls, the file
provenance, the identity) in one read-only transaction; dangling tool calls
are kept, an unknown id is loud, and the recorder adopts the existing row so
one identity serves todo's claims and rem's sources. The per-process state
starts fresh: the guard's counts and the steering slot are not persisted.

**On the allow-list** — it is default-deny below it: any tool not named is
refused at the boundary and the refusal is fed back to the model. The default
permits the thirteen built-in tools because a default-deny CLI would ship a
dead agent; narrow with `--allow read` or similar.

## terminal

The default REPL is the terminal frontend (`specs/SPEC_TUI.md`): the
same session, the same commands, the same exits — themed, with the
banner (model, effort, context, session usage), the live region (the
activity row and the input line, redrawn in place), the todo and
scheduler blocks, and the usage line at every turn's end. The piped CLI
is unchanged and is the reference: pipe, `-p`, and `--tui=false` all
speak the CLI's bytes.

- `--tui auto` (the default) picks by the terminal: stdout a terminal
  → the TUI; piped or redirected → the CLI. `--tui=true` forces the
  TUI (a pty, `tmux capture-pane`); `--tui=false` forces the CLI.
- **Theme** — `~/.config/rig/theme.json`, three keys: `base` (the
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
./rig --version                 # prints: rig 0.3.0
./rig --base-url $YOUR_ENDPOINT --model $NAME --system "be terse"
```

then type a prompt. A line typed while a turn is live steers (it interrupts
the turn and is delivered at the next prompt; latest wins); Ctrl-C ends the
session once the in-flight step unwinds; Ctrl-D exits at the prompt. If
nothing streams, check the endpoint first — rig surfaces provider faults
verbatim and loudly; it does not hide them.

The terminal's verify: the banner's two rows (model and context), a
streaming turn with its activity row, a tool line with its glyph and
duration, the usage line at the turn's end, and a clean Ctrl-D exit.
`--tui=true` under `tmux` gives the same session to `capture-pane`
(piped `--tui=false` stays the byte reference).
