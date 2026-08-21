# rig serve: the local dashboard in the binary

A subcommand beside `run-job`: `rig serve`. It opens a local dashboard on
the rig home's stores: the operator watches the same SQLite files a live
session is writing (sessions, todo, rem, the scheduler), reads the models
table and the plugin set, and has one write door (a todo create). One binary,
no framework, no build step: `go build` alone produces the working server.

The dashboard is a *reader*, not a second copy. It opens the exact store
files the root opens (the same rig home, the same pragmas) and calls the
same store verbs a live session's tools call. A live session writes; the
dashboard reads the same rows. WAL plus busy_timeout carries the
cross-process safety, exactly as the scheduler's runner already relies on.

Baseline is 0.9.0 (main at the plugin door). This is phase 1 of a
multi-phase surface: the read dashboard plus the one write. The later
phases (named non-goals) add more writes, a live session, the python
shell, and the plugin forge.

## goals

- **A subcommand.** `rig serve [-addr 127.0.0.1:7777]`, wired in `cmd/rig`
  beside `run-job`. One file plus one registration line at the root; the
  loop and the REPL are untouched.
- **A new leaf package `frontend/web`.** The server, the handlers, and the
  `go:embed` static assets (plain HTML/CSS/JS, no framework, no build step,
  no external asset). It depends on the store verbs, `models`, `plugins`,
  and stdlib `net/http`. It never names a concrete provider, tool, or loop
  type.
- **Reads, verbatim.** Sessions, todo, scheduler, rem, models, and plugins
  are the model's own data, read through the same store verbs and shown
  as-is: todo and scheduler as the model's own text (a `<pre>`), sessions
  rendered as structure, models and plugins as tables.
- **One write.** A todo create form (one task per line) that calls
  `todo.Create` with session `dashboard` and shows the verb's reply
  verbatim. The same event a live session's `todo` tool would append,
  attributed to `dashboard`.
- **A posture the spec can hold.** Loopback-only, a minted bearer token, a
  bounded and allow-listed handler set, an Origin-checked write, and bounded
  reads. Deny by default, fail closed on uncertain state.

## non-goals (phase 1)

- No other writes. More verbs (a todo transition, a scheduler create, a
  rem learn) are phase 2, through the existing verbs only. This phase
  mutates exactly one row family (the todo event log, one create).
- No live session. Streaming a running session's events into the page
  (the `Frontend` seam, phase 3) is not here. The dashboard reads landed
  rows; it does not observe a live turn.
- No python shell, no plugin forge (phase 4). The dashboard lists plugins
  and their DESCRIPTION; it does not execute them.
- No editing `settings.json` from the browser (never). The models and
  plugin views are read-only.
- No multi-user. One operator, one token, one machine.
- No TLS, no WebSocket, no SSE, no streaming. Plain request/response over
  loopback HTTP.
- No new dependencies. `go.mod` unchanged; the assets are embedded text.

## layout

```
frontend/web/            NEW: the dashboard leaf
  web.go                 Options, Server, New, Handler, Serve, Close
  auth.go                the serve.token mint/read and the bearer+cookie gate
  routes.go              the allow-list router and the view handlers
  stores.go              cwd -> store path resolution and the open cache
  plugins.go             the loaded + pending listing, the DESCRIPTION read
  static.go              the go:embed of static/
  static/
    index.html           the single page, the sidebar nav, the cwd picker
    style.css            pane's design language (the oled palette's values)
    app.js               the fetch loop, the render, the create form
  web_test.go            the named cases (httptest over a temp rig home)
  PACKAGE.md
cmd/rig/
  serve.go               the serve subcommand: flags, the rig home, config,
                         the web.Options, the loopback refusal, the run
  main.go                the one registration line (serve, beside run-job)
                         and the state/todo path helpers' use
store/
  state/                 +SessionRow.Cwd, +SessionUsage (the typed usage
                         read), +StorePath (home, cwd -> the file)
  todo/                  +StorePath (home, cwd -> the file)
  rem/                   +FilePath (home -> the file)
frontend/tui/
  freeze_test.go         +frontend/web to the allow-list, with the round's
                         comment
specs/
  SPEC_SERVE.md          this file
README.md                three lines
```

`core/`, `loop/`, `middleware/`, `policy/`, `provider/`, `command/`:
untouched.

## the seams it uses, verbatim

The dashboard calls store verbs, never domain accessors and never raw SQL.
A view that later wants a table exports a typed fold beside the string
(`todo.Queue`, `scheduler.Jobs`) from the same fold; not this phase. This
phase reads exactly these verbs:

- **Sessions (typed).** `state.ListSessions(ctx, db)` for the list, grouped
  by workspace; `state.Resume(ctx, db, id)` for one session's full
  transcript (messages, reasoning, tool calls and results), rendered as
  structure; `state.SessionUsage(ctx, db, id)` for the session's usage rows.
  `SessionRow` gains `Cwd` so the list groups by workspace and the cwd
  picker can enumerate the workspaces that have sessions.
- **Todo (the model's own text).** `todo.Read(ctx, db, "dashboard")` and
  `todo.ReadAll` returned as-is and shown in a `<pre>`; the operator sees
  exactly what the model sees, stale footer included. The one write:
  `todo.Create(ctx, db, items, "dashboard")`, the reply verbatim.
- **Scheduler (the model's own text).** `scheduler.List(ctx, st, ct,
  sessionCwd, probe, now)` returned as-is and shown in a `<pre>`, drift
  notes included. `probe` reports a held lock (running); `ct` is the real
  crontab.
- **Memory.** `rem.Recent(ctx, db, cwd, k)` for the selected cwd, shown as
  a list.
- **Models.** `cfg.Models` (the `models.Table` from config), every row with
  its window, effort list, and role, shown as a table.
- **Plugins.** `plugins.List(home)` (the loaded set) plus the pending zone
  (`home/plugins/pending/`), each by name with the file's `DESCRIPTION`.

The store is opened the way the root opens it: `store.Open(path,
Statements(), SchemaVersion)` over the same file, the same pragmas. The
dashboard's cwd picker re-resolves the per-cwd paths (`state.StorePath`,
`todo.StorePath`, `scheduler.StorePathFor` + `scheduler.CwdHash`, and
`rem.FilePath`) and caches the open DBs per file.

## the one write

A todo create: a form of one task per line. The handler trims blank lines,
builds `[]todo.CreateItem` (text only; no dependsOn in phase 1), and calls
`todo.Create(ctx, db, items, "dashboard")`. The reply string (the store's
note and the queue, the store's own voice) is returned verbatim to the page.
An empty form is a loud refusal (no create with zero tasks). No other route
mutates anything in this phase; the write is the only `db.Tx` (not
`TxReadOnly`) the dashboard opens.

## posture

- **Loopback only.** `-addr` must name a loopback interface (`127.0.0.1`,
  `::1`, or `localhost`); a non-loopback bind is refused by name before the
  listener opens. `tailscale serve` (an explicit tunnel) is the way out, not
  a wider bind.
- **A minted token.** On first run a bearer token is minted into
  `~/.rig/serve.token` (0600) and printed once (stderr). On later runs the
  token is read, not re-minted (the operator keeps the same credential).
  Every request carries it: the `Authorization: Bearer` header, or a
  one-time `?token=` that sets an `HttpOnly; SameSite=Strict` cookie and
  then reads as the cookie.
- **The token gate.** No credential, or a wrong one, is a 401 (with
  `WWW-Authenticate: Bearer`). The comparison is constant time. The token
  is never echoed into a response body or a log line.
- **The write is bounded.** The create route is POST only, Origin-checked
  against the bound address (same-origin only, no CORS), and the body is
  size-capped (`MaxBytesReader`). A read route that receives a POST, or a
  write from a foreign Origin, is a refusal, not a silent no-op.
- **The handler set is an allow-list.** Every (method, path) is named; an
  unknown path is a 404 and a known path with the wrong method is a 405
  (with the `Allow` header). There is no catch-all beyond the named static
  assets.
- **Reads are bounded.** Each read handler runs under a read timeout; the
  transcript is paginated (a limit and offset over `Resume`'s messages, not
  the whole history every call).
- **Fail closed.** A store that will not open, a cwd that resolves to no
  file, or a token that will not read is a named refusal, never a silent
  empty page.

## the views

One page, a sidebar nav, and a cwd picker at the top that drives the
todo/rem/scheduler/sessions views:

- **Sessions.** The selected workspace's sessions, newest first (id,
  started, exit, turns); open one for the transcript (messages, reasoning,
  tool calls and results, and the usage rows) as structure.
- **Todos.** The selected cwd's queue, the actionable view by default and a
  history toggle for `ReadAll`; the create form (one task per line) and the
  verbatim reply.
- **Scheduler.** The list, both scopes, drift notes included.
- **Memory.** The selected cwd's recent memories.
- **Models.** Every row: id, window, max tokens, reserve, keep-recent,
  role, effort, and the effort list.
- **Plugins.** The loaded set and the pending zone, each by name with its
  DESCRIPTION.

The theme is pane's design language: the page reuses the oled palette's
hex values (the `frontend/tui` `theme.go` shipped table) for background,
text, dim, accent, success, error, warn, and rule.

## decisions

### 1. A leaf package, not a new seam.

`frontend/web` is a frontend in the layout sense (a way to reach the
state), not a `core.Frontend` registration: it does not drive the loop and
the loop does not name it. It sits beside `cli`/`tui`/`oneshot` and is
wired only at the root. Adding it touches no loop, core, or middleware
line.

### 2. The dashboard is a reader of the same files.

It opens the same SQLite files (the same rig home, the same pragmas, the
same `store.Open`) and reads them with the same verbs. WAL plus
busy_timeout is the cross-process story; the dashboard never copies or
forks a store, and a live session's writes are visible on the dashboard's
next read. The per-cwd path formulas move into the stores
(`state.StorePath`, `todo.StorePath`, `rem.FilePath`) so the root and the
dashboard share one source; the root's inline formulas are replaced by the
helpers (no behavior change).

### 3. Store verbs, never raw SQL.

The named decision, held: the dashboard calls `ListSessions`, `Resume`,
`SessionUsage`, `Read`, `ReadAll`, `Create`, `scheduler.List`, `Recent`,
and the `models.Table`/`plugins.List` surfaces. The two small store
additions (`SessionRow.Cwd`, `SessionUsage`) are typed verbs the dashboard
consumes, not a SQL leak. When a view outgrows the string (a todo or
scheduler table), the store exports a typed fold beside the string from the
same fold; that is a later phase.

### 4. One write, attributed.

The todo create is the only mutation and it rides the existing verb with
session `dashboard`, so the event log carries the attribution a `todo`
tool would and the operator's create is distinguishable from the model's.
The reply is shown verbatim (the store's voice is the contract). No other
verb is reachable in phase 1.

### 5. Loopback and the token are the two walls.

Binding is refused by name when it is not loopback, and every request must
carry the minted token (header or the one-time `?token=` cookie). The token
lives at `~/.rig/serve.token` (0600), minted once and printed once. The
write adds an Origin check and a body cap on top. This is the whole
posture; there is no CORS, no multi-user, and no TLS to reason about.

### 6. Structure for sessions, verbatim text for the rest.

Sessions are rendered as structure (the transcript as JSON the page lays
out); todo and scheduler are shown as the model's own text in a `<pre>`
(the operator sees exactly what the model sees, stale footer and drift
notes included). The split is the seam shape: a typed projection is
rendered as structure, a store's voice is shown verbatim.

## testing

Named cases, failing first, against `httptest` over a temp rig home with
seeded stores (the existing test seams; a real sqlite file, never
`:memory:`):

- **The token gate.** No credential is a 401; a wrong token is a 401; the
  minted token (header) is a 200; `?token=` sets the `HttpOnly
  SameSite=Strict` cookie and then reads as the cookie; the token file is
  0600 and is not re-minted on a second open.
- **The loopback refusal.** `127.0.0.1`, `::1`, and `localhost` are
  accepted; a non-loopback `-addr` is refused by name before the listener
  opens.
- **The write's walls.** The create route is POST only (a GET is a 405); a
  foreign `Origin` is refused; a same-Origin POST succeeds and returns the
  verb's reply verbatim; an over-cap body is refused; the create lands one
  event attributed to `dashboard`.
- **The allow-list.** An unknown path is a 404; a known path with the wrong
  method is a 405 with the `Allow` header.
- **The reads.** The sessions list groups by workspace and carries the cwd;
  the transcript renders as structure (a golden over a seeded session:
  messages, reasoning, tool calls and results, and the usage rows); todo
  `Read`/`ReadAll` return the store's text verbatim; scheduler `List`
  returns the store's text (drift included); memory `Recent` returns the
  cwd's rows; the models table carries every row's effort list and role;
  the plugins view lists the loaded and pending sets with DESCRIPTION.
- **Fail closed.** A cwd that resolves to no store file is a named refusal,
  not an empty 200.

The store additions carry their own cases: `SessionRow.Cwd` is populated;
`SessionUsage` returns the seeded usage rows; `StorePath`/`FilePath`
resolve to the same file the root opens (a round-trip: open via the helper,
read back what was written).

The suite is green on a box with no model and no python: the dashboard is
pure Go over the stores, the config, and the plugin files.

## the diffs this spec implies

- **`store/state`**: `SessionRow` gains `Cwd`; `SessionUsage` (the typed
  usage read); `StorePath(home, cwd)`.
- **`store/todo`**: `StorePath(home, cwd)`.
- **`store/rem`**: `FilePath(home)`.
- **`frontend/web`**: the new leaf (server, auth, routes, the store cache,
  the plugins listing, the embedded assets) plus its `PACKAGE.md`.
- **`cmd/rig`**: the `serve` subcommand (one file) and its one registration
  line; the root's inline state/todo path formulas use the new helpers.
- **`frontend/tui/freeze_test.go`**: `frontend/web` added to the allow-list
  with the round's comment.
- **`README.md`**: three lines (the subcommand, the token, the one write).

## scope

What this is not:

- Not a second copy of the state, not a cache, not a server the loop talks
  to: a reader of the same files, wired once at the root.
- Not more writes in phase 1: one todo create, through the existing verb.
- Not a live view, not a shell, not a forge: those are the named later
  phases.
- Not a change to the loop, core, or middleware: the design test holds;
  `frontend/web` is one file plus a registration line at the root.
