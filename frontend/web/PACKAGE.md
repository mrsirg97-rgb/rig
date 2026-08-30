# frontend/web

## What it is

The local dashboard (SPEC_SERVE): a loopback-only `net/http` server that
reads the rig home's stores and renders them as one embedded page. It is
a *reader* of the same SQLite files a live session is writing; the same
rig home, the same pragmas, the same store verbs, and it carries the
operator's writes beside the model's tools (the todo create and two
hands, the scheduler create and four doors, the plugin create and the
forge's doors), each attributed to `dashboard` and riding the existing
verb or the provenance rule's landing zone. It is a leaf package wired
once at the root, beside `cli`/`tui`; the loop never names it. No
framework, no build step, no external asset: `go build` alone ships it.

## What it includes

- **`Server` / `New` / `ListenAndServe` / `Handler` / `Close`**: the
  dashboard: the static inputs (the rig home, the serve cwd, the models
  table, the crontab, the runner command), the token, the allowed
  origins, the store cache, the rooted static assets.
- **The token gate** (SPEC_SERVE 5): `serve.token` (0600) minted once and
  printed once; `Authorization: Bearer`, the one-time `?token=` (which then
  sets the cookie), or the cookie; a constant-time compare; a 401 with the
  `WWW-Authenticate: Bearer` challenge.
- **The loopback refusal**: `-addr` must name a loopback interface
  (`127.0.0.1`, `::1`, or `localhost`); a non-loopback bind is refused by
  name before the listener opens.
- **The allow-list router**: every (method, path) is named; an unknown
  path is a 404 and a known path with the wrong method is a 405 (with the
  `Allow` header); the static assets are the only non-API surface.
- **The read views**: sessions (the list, grouped by workspace, and the
  transcript as structure: messages, reasoning, tool calls and results,
  and the usage rows), todo and scheduler (the store's own text,
  verbatim; the scheduler reply carries the fleet's model as `worker`,
  empty when no fleet stands, so the view renders the create form or
  the no-fleet refusal in its place), models (every row: window, effort
  list, role), plugins (the loaded set, the pending zone, and the
  disabled zone, each with the file's DESCRIPTION). The memory
  view is gone (SPEC_SERVE 11): rem is the model's, read in the TUI.
- **The writes** (SPEC_SERVE phase 2): a todo create (one task per
  line, `todo.Create`), a scheduler create (`scheduler.Create`, the
  runner command the root wired; no fleet refuses 400 naming the
  `workers.json` move, a present fleet supplies the model when the
  body names none), and a plugin create (one file into
  `plugins/pending/`, the contract's `DESCRIPTION`/`SCHEMA`/`run`). Each
  is Origin-checked (same-origin only), body-capped, POST-only, the
  reply verbatim; the only `db.Tx` (not `TxReadOnly`) the dashboard
  opens (the plugin write is a file write the provenance rule blesses).
- **The store cache**: opens the store files the way the root does (the
  stores' own path helpers, one shared source) and caches them per file;
  a reader of the same files, never a second copy.
- **The plugin listing, live** (SPEC_SERVE phase 2, decision 8): read
  per request from the home's `plugins/` and `plugins/pending/`, never
  cached at `New`: a file created, promoted, or dropped is visible on
  the next read.
- **The todo's two hands** (`todoverbs.go`, SPEC_SERVE 15): `POST
  /api/todo/start` and `/api/todo/complete` (`{id}`): `todo.Start` and
  `todo.Complete` attributed to `dashboard`, the id checked to the
  tool's shape, the reply verbatim (a model-claimed task refuses in the
  store's voice). Same walls as every write.
- **The scheduler's four doors** (`schedulerverbs.go`, SPEC_SERVE 16):
  `POST /api/scheduler/pause`, `/resume`, `/remove` (`{id}`) and
  `POST /api/scheduler/update` (`{id, …}`, the same partial fields the
  tool carries), plus the read `GET /api/scheduler/runs?id=jN&n=` (the
  audit trail). Each door calls the store verb the `scheduler` tool
  calls (`scheduler.Pause`/`Resume`/`Remove`/`Update`/`Runs`) with the
  selected cwd as the session cwd, the attribution `dashboard`, and the
  runner command the root wired (for `update`); the id is checked to
  the tool's shape (`jN`, a bad id a 400) and the store's refusals ride
  through by name (an unknown id, a removed one, an update with no
  change). The POST doors ride the write's walls (Origin, the body
  cap); `runs` rides the read's (the read timeout, `n` capped at 1-100,
  an unknown id the named 404). The reply is the store's voice,
  verbatim.
- **The forge** (`forge.go`, SPEC_SERVE 12): `GET /api/plugins/source`
  (a plugin's file, by name and zone), `POST /api/plugins/save` (the
  full source into the pending zone, create or update; the contract:
  `DESCRIPTION`, `SCHEMA`, `def run(`; checked; a native name refused),
  `POST /api/plugins/approve` (pending → `plugins/`, the command's
  verb; a native name refused; an installed name a 409 until `replace`
  is explicit), `POST /api/plugins/disable` and `POST
  /api/plugins/enable` (the command's move between `plugins/` and
  `plugins/disabled/`, each calling `plugins.Move`, each replying in
  the command's voice; a plugin not in the source zone is the named
  404, a duplicate in the target zone the named 409). Same walls as
  every write.
- **The folder browser** (`browse.go`, SPEC_SERVE 13): `GET /api/fs`:
  directories only, rooted at `Options.Root` (the user's home by
  default), symlinks resolved before the root check, hidden entries off
  unless asked, the listing capped at 500, a path outside the root a
  403 by name.
- **The static assets** (`static/`): the single page in the TUI's own
  grammar (decision 14): every view is a tool block (`● name · detail`,
  the body, `name ✓`), input is a `❯` prompt row, the nav marks the
  active view with `❯`, nothing sits in a panel; the oled palette's
  values and the effort ramp (the `frontend/tui` `theme.go` table), the
  glyph set, the todo and scheduler text parsed by the
  `tools_render.go` rules mirrored in JS (decision 10; unparseable text
  falls back to the verbatim `<pre>`), the models view in the `/models`
  table's shape with the effort list in the ramp's colors, the plugins
  view split approved | pending | disabled with the forge's editor (a
  gutter, a highlighted mirror over a transparent textarea; Tab
  indents, Enter keeps the indent), the phone rule (every loaded row a
  disable control, every disabled row an enable one, the list re-read
  after the move; 12c), the scheduler view's row hand (16: every job
  row carries its controls; pause or resume by state, remove, and
  runs; beside an update form that opens in place with the row's
  current fields (cadence, prompt, model, cwd, busy) and submits only
  what changed; remove asks once, in-page; the list re-reads after a
  move, no page reload; below 720px the row's controls stay a
  horizontal wrapping row of 44px tap targets; never a full-width
  stack; the form is one column, and the buttons stop propagation so
  a tap never opens two things), the folder browser
  under the header, the mobile drawer below 720px (its toggle hidden on
  desktop), the cwd picker with the new-workspace add (client state,
  decision 9).

## How it is consumed

- The root wires it with `web.New(web.Options{Home, CWD, Models,
  Crontab, RunnerCmd})` and `srv.ListenAndServe(ctx, addr)`, behind the
  `serve` subcommand (`cmd/rig/serve.go`, one file plus a registration
  line in `main.go`).
- The models table is `cfg.Models` (the config's table, every row): the
  crontab is `sched.RealCrontab("")` (injected in tests); the runner
  command is `<self> run-job` (the store's `rig run-job` when empty).
- The operator opens `http://127.0.0.1:7777/?token=<printed>` once (the
  cookie is set); the same-origin fetches then carry it.

## The seams it uses, verbatim

The dashboard calls store verbs, never domain accessors and never raw SQL
(SPEC_SERVE 3): `state.ListSessions`, `state.Resume`,
`state.SessionUsage`, `todo.Read`, `todo.ReadAll`, `todo.Create`,
`todo.Start`, `todo.Complete`, `todo.Retry`, `scheduler.List`,
`scheduler.Create`, `scheduler.Pause`, `scheduler.Resume`,
`scheduler.Remove`, `scheduler.Update`, `scheduler.Runs`, `rem.Recent`,
`plugins.Move`, and the `models.Table` / `plugins.List` surfaces. The
two small store additions (`SessionRow.Cwd`, `SessionUsage`) are typed
verbs the dashboard consumes, not a SQL leak. The plugin create writes
the file the provenance rule already blesses (`plugins/pending/`), then
reads it back through the same listing.

## Gotchas

- The path formulas are the stores' own: `state.StorePath`, the one
  `todo.FilePath`, `rem.FilePath`, and the scheduler's one
  `<home>/scheduler/global.sqlite` (the root's home, not the rig home
  directly). The root's inline formulas were replaced by these helpers
  (one source). The todo store is one file, and `/api/todo?cwd=`
  resolves the selected cwd's scope through the same law as the model's
  tool (SPEC_STATE).
- `todo.Create` is an upsert (it adds the given tasks and keeps the
  existing queue); the reply is the store's voice, shown verbatim. The
  dashboard does not assert a replace; it shows what the verb says.
- The sessions store defines a workspace: a read of a cwd with no state
  file is a named 404 (fail closed); the todo/memory/scheduler reads open
  (creating if absent) and show the store's empty voice.
- The transcript is paginated (`limit`/`offset`) over `Resume`'s messages;
  `Resume` itself loads the full transcript, so a very long session loads
  fully before the page is sliced (phase 1's bound).
- The token is minted on first use and printed once (stderr): a second
  open reads it, never re-mints. The token never rides a response body.
- The write's Origin must name the bound address (and `localhost` when the
  bind is loopback); the origins are set from the bound address at serve
  time (set directly in tests).
- The scheduler create rides `scheduler.Create` verbatim: the crontab is
  the scheduling truth, so a create installs a line in the real crontab
  (the runner command the root wired); a duplicate name store-wide is the
  verb's named refusal, and the reply is the store's voice. The selected
  `cwd` is passed to `List`/`Create` only for the job's own `cwd` field
  and the list's this-directory-first ordering.
- The scheduler's doors ride their verbs the same way (16): the selected
  `cwd` is the session cwd the verbs take, and the store's refusals ride
  through by name; the dashboard invents no refusal of its own beyond
  the id's shape (`jN`) and the `runs` read's `n` cap (1-100). `update`
  is partial: only the fields the body carries change, so the page
  submits only what the operator changed (the row carries no prompt, so
  an empty prompt is no change). `runs` is a read of the one global
  store: no `cwd`, no Origin wall, the audit trail verbatim.
- The plugin create is the operator's authoring door, not the model's:
  the file lands in `plugins/pending/` (the provenance rule's landing
  zone), never in `plugins/`; promotion (move it up, reload) stays the
  operator's step. The name is the filename stem (lowercase, leading
  letter), refused when it exists in either zone; the generated file is
  the contract with an empty `SCHEMA` object until extended.
- The plugin listing is live (decision 8): `New` no longer caches it, so
  a listing reflects the files at request time; a bad `DESCRIPTION` read
  is an empty description, never an error (one bad file must not brick
  the listing).
- The new-workspace picker is client state (decision 9): added cwds and
  the selected cwd live in the browser (localStorage), merged over the
  server's list; the server's list stays the truth for what exists. The
  folder browser feeds it: a pick is an add, nothing more.
- A save always lands in `plugins/pending/`: also an edit of a loaded
  plugin, which becomes a pending revision under the same name; approve
  then refuses with a 409 until `replace: true`, the one explicit
  overwrite. Nothing reaches `plugins/` except through approve.
- The mobile nav toggle is `.nav-toggle` (hidden above 720px by class,
  not by id; the first round's button carried only the id and showed
  on desktop).
- The homage's parsers mirror `frontend/tui/tools_render.go` line for
  line (decision 10): the todo head/task/footer rules, the scheduler
  section/job/detail rules, the bar's fill, the glyph and slot mapping.
  The JS todo render keeps the reply's `→` action line as the block's
  opening (the TUI's opening line, which the TUI substitutes for it).
  Text the rules do not parse is the verbatim `<pre>`, never a broken
  page.

## Tests

The named cases (SPEC_SERVE, testing), `httptest` over a temp rig home
with seeded stores (the store path helpers are the round-trip): the token
gate (401s, the bearer, the `?token=` cookie, the 0600 file, no re-mint);
the loopback refusal (accepts and refuses by name); the allow-list (404,
405 with `Allow`); the writes' walls (Origin, body cap, empty body, the
verbatim reply, the upsert; for the todo, the scheduler, and the plugin
creates, plus the scheduler's duplicate/cron/once refusals, the plugin's
name/duplicate/empty-body refusals, and the live listing); the
disable/enable doors (both move the file and reply in the command's
voice, a disable of a pending plugin refuses, a duplicate in the target
zone the named 409, the Origin wall, the 405); the scheduler's doors
(each moves the store and replies in the store's voice verbatim; pause
marks the job paused in the next list read, remove drops it, update
changes the field the reply names; the runs read returns the seeded
audit trail with the `n` cap and the unknown id the named 404, the
store's refusals by name: an unknown id, a removed one, an update with
no change; the walls: Origin, the body cap, a bad id, the 405s); the
reads (the
cwds, the sessions list and cwd, the transcript golden; messages,
reasoning, tool calls and results, and the usage rows; the
todo/scheduler verbatim text, the memory, the models rows with effort and
role, the plugins with DESCRIPTION), and the static assets (served, 404,
traversal refused, the drawer's toggle, the picker's add, the homage's
parsers and bar, the scheduler's row controls and the in-place update
form with the horizontal phone row of controls). The store additions carry their own cases in
`store/{state,todo,rem}`. The JS parsers' parity with the Go ones is
checked by hand against the same seeded text (the node harness, not a
suite member).
