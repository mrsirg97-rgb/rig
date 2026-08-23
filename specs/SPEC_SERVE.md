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
- **Memory.** `rem.List(ctx, db, cwd, k)` for the selected cwd (the live
  rows, project then global), shown as a list. (`rem.Recent` is gone with
  the injection, 0.13.0.)
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
`SessionUsage`, `Read`, `ReadAll`, `Create`, `scheduler.List`, `rem.List`,
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
  returns the store's text (drift included); memory `List` returns the
  cwd's live rows; the models table carries every row's effort list and role;
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

## phase 2: the polish round

Phase 1 shipped the read dashboard and the one write. This round is the
polish: two more writes through the existing verbs (the named phase 2
scheduler create and the plugin forge's first cut), the page made mobile,
and the page's design language turned into a deliberate homage to the
TUI — each view renders the way the TUI renders that tool's output.

### goals

- **The scheduler create.** `POST /api/scheduler` calls
  `scheduler.Create` with session `dashboard`, the same verb a live
  session's `scheduler` tool calls. The form carries name, prompt, cron
  (five fields, or `once` plus an ISO `at`), and the scope (the page's
  cwd by default, global on request). The verb's reply is shown
  verbatim, the list re-read after.
- **The plugin create.** `POST /api/plugins` writes one file into the
  pending zone (`plugins/pending/<name>.py`), the provenance rule's
  landing zone (SPEC_SANDBOX 2): the operator's creation is reviewable,
  never silently live. The file is the plugin contract (SPEC_PLUGINS) —
  a `DESCRIPTION`, a `SCHEMA` (an empty object until the operator
  promotes and extends it), and a `run(args)` whose body the form
  supplies. The name is the filename stem: lowercase, digits and
  underscores, leading letter, and it must not already exist in either
  zone (a duplicate is a named refusal, no overwrite).
- **The cwd picker accepts new workspaces.** The page can add any
  workspace path (persisted in the browser), not only the ones that
  already have a session; the reads already take any cwd, and a cwd with
  no state store keeps its named 404 (fail closed). No server state:
  the picker is the page's, the stores are the truth.
- **Mobile.** The sidebar collapses into a drawer below 720px (a
  toggle in the header, the backdrop closes it); the header, forms, and
  tables wrap and keep 44px touch targets. Desktop is unchanged.
- **The TUI homage.** The page reuses the TUI's theme values (already
  the case) and its visual grammar: the todo view renders the store's
  text with the TUI's todo render (the progress bar, the
  `done/total · next · failed` head, the status glyphs, the dim
  `waits on`/`claimed by` tails); the scheduler view renders it with
  the TUI's scheduler render (the scope sections, the state glyphs,
  the dim detail lines, the warn `drift:`); sessions and the transcript
  adopt the tool-block shape (the `✓ name · detail` opening, the `→`
  result, the `❯` prompt for the user's line, the aggregated usage
  row); the memory and plugins views adopt the same block grammar. The
  parse rules mirror `frontend/tui/tools_render.go`; text the rules do
  not parse falls back to the verbatim `<pre>` (the TUI's own rule).

### decisions

### 7. Two more writes, the same walls as the first.

The scheduler and plugin creates are POST-only, Origin-checked,
body-capped, and ride the existing verbs (`scheduler.Create`; a file
write into the pending zone the plugin rule already blesses). The
attribution is `dashboard`, as with the todo create. The scheduler
create's runner command is the root's own (`<self> run-job`), injected
through `web.Options.RunnerCmd` exactly as the crontab is — the page
never assembles the command line.

### 8. The plugin listing is live.

`GET /api/plugins` re-reads the directory on every request instead of
caching the `New`-time list: a file created by the form, promoted by
the operator, or dropped by another tab is visible on the next read,
with no reload of the serve process. The listing stays a read of the
files (name from the stem, the file's `DESCRIPTION`), not discovery —
no kernel, no execution.

### 9. The new-workspace picker is client state.

The added workspaces live in the browser (localStorage), merged over
the server's list on load. The server's list stays the truth for what
exists (the serve cwd, the workspaces with sessions); the page's
additions are the operator's bookmarks. A read of an added cwd with no
state store is the named 404, unchanged.

### 10. The homage is a render, not a new wire.

The page parses the store's verbatim text with the TUI's own rules and
renders it in the oled slots; the wire keeps the store's voice
(phase 1's decision 6 holds — the text is unchanged, only the
presentation is structured). The JS parsers mirror the Go parsers line
for line; a parse failure is the verbatim fallback, never a broken
page. No new endpoint, no new verb, no new dependency.

### testing

The phase 1 cases stay green, plus (failing first, `httptest` over the
seeded temp home):

- **The scheduler create.** A same-Origin `POST /api/scheduler` with
  name/prompt/cron returns the verb's reply (`created jN 'name'
  (scope)`) and the list carries the job; a `once` plus a valid `at`
  lands; a duplicate name in the same scope is a named refusal; a bad
  cron is the verb's refusal; a no-Origin or foreign-Origin write is a
  403; an over-cap body is a 400; `DELETE /api/scheduler` is a 405 with
  `Allow` naming POST.
- **The plugin create.** A same-Origin `POST /api/plugins` with
  name/description/code writes `plugins/pending/<name>.py` carrying the
  `DESCRIPTION`, a `SCHEMA` object, and a `def run(args):`; the pending
  listing then carries it (the live listing); a duplicate name in
  either zone is a named refusal; a bad name (uppercase, leading
  digit, a slash) is a 400; an empty code is a 400; the walls (Origin,
  cap) hold as with the todo create.
- **The static assets.** The page carries the drawer's toggle and the
  new-workspace affordance; the parsers for the todo and scheduler
  text, the bar and glyph renderers, the add-cwd and nav-toggle
  handlers, and the drawer's styles are present in the shipped assets.

### the diffs this phase implies

- **`frontend/web`**: the two write handlers, `Options.RunnerCmd`, the
  live plugin listing, the rewritten static assets (the mobile shell,
  the homage renderers, the picker), the `PACKAGE.md` update.
- **`cmd/rig/serve.go`**: the one added option (the runner command).
- **`specs/SPEC_SERVE.md`**: this section.
- **`CHANGELOG.md`**: the phase 2 entry.

The TUI freeze gate needs no new allowance: the round touches
`frontend/web`, `cmd/rig`, and `specs/`, all named.

## phase 2, second pass: the TUI's grammar, the forge, the browser

The first polish round put the TUI's palette on the page; this pass puts
the TUI's grammar on it and closes the plugin loop from the browser.
Four issues from the phone and the desk, and two doors.

### 11. The memory view is gone.

`rem` is the model's store: the model learns, recalls, and consolidates
it through its tool, and the operator reads it in the TUI when it
matters. A per-workspace list of recent notes on a dashboard was noise
beside the views the operator acts on; the route (`/api/memory`) and the
tab are removed, the seed stays in the tests (the store is unchanged).
Rejected, named: a read-only rem browser (it would want search, scopes,
and supersession to be honest — a fourth phase, if ever).

### 12. The forge: source, save, approve.

The plugin page splits into approved | pending (approved by default,
the counts in the toggle) and opens a plugin into an editor: a gutter,
a highlighted mirror over a transparent textarea (Tab indents four,
Enter keeps the indent and adds one after a colon), no dependency.
Three doors, the same walls as every write (POST, Origin, the body cap,
the name wall):

- `GET /api/plugins/source?name&zone` — the file, verbatim, by zone.
- `POST /api/plugins/save {name, source}` — the full source into
  `plugins/pending/<name>.py`, create or update; the contract is checked
  by presence (`DESCRIPTION`, `SCHEMA`, `def run(`); a native name is
  the collision refusal. An edit of an approved plugin saves a pending
  revision under the same name — nothing reaches `plugins/` by saving.
- `POST /api/plugins/approve {name, replace?}` — the command's verb,
  verbatim: pending → `plugins/`; a native name refused; an installed
  name a 409 naming `replace` until it is explicit, then the swap. The
  reply names what a live session needs: its next `plugins reload`.

The phase-2 create form (`POST /api/plugins`, the run body wrapped)
stays as a door; the page now uses the editor with the contract's
template. Rejected, named: saving straight into `plugins/` from the
editor (the operator's hand is the approve verb, not the save; one door
for promotion keeps the provenance rule one rule); executing the plugin
from the page (the shared kernel is a session's; phase 4).

### 12b. The disabled zone (0.12.1).

The plugins page lists three zones — approved | pending | disabled —
the three directories (SPEC_GROWTH 9, amended). A disabled plugin
opens in the editor like any other (its zone is `disabled` for
`source`); a save is a pending revision as ever. The page does not
move files between zones: that is the session's `/plugins
enable|disable` verb (an operator act the dashboard could carry later,
named).

### 13. The folder browser, rooted at home.

The picker's "add a workspace" gains a browser: `GET /api/fs?path` lists
directories only, rooted at the user's home (`Options.Root`, the tests
inject one), symlinks resolved before the root check (a link out of home
is outside), hidden entries off unless `hidden=true`, the listing capped
at 500 with a named truncation, a path outside the root a 403 by name, a
file a 400, an absent path a 404. A pick is an add (decision 9): the
page's bookmark, nothing written. Rejected, named: browsing `/` (the
dashboard is a reader of the operator's work, not the box), listing
files (the picker wants folders), following links out of home.

### 14. The page speaks the TUI's grammar.

Every view is a tool block — `● name · detail`, the body, `name ✓` —
the shape `frontend/tui` commits (commit.go); input is a `❯` prompt row
with a rule under it, never a boxed field; the nav marks the active view
with `❯`; nothing sits in a panel. The models view takes the `/models`
table's line (`id  role  window … trigger …`) with the effort list in
the ramp's slots, the row's default underlined. The sessions list is
rows you click, the transcript's tool calls open `● name · detail` like
the TUI's and show raw args only when no detail parses. The mobile nav
toggle is hidden above 720px by its class (the first round's button
carried only the id and showed on desktop — the one bug in the list).

### testing

The phase 1 and 2 cases stay green, minus the memory read (now the 404
case), plus (failing first): the forge's source by zone and its
refusals; save creates then updates, checks the contract, refuses a
native name, and holds the Origin wall; approve moves the file, finds
nothing the second time, 409s over an installed name until replace, and
refuses a native; the browser's root listing (folders only, hidden off,
no parent), hidden on request, a child with its parent, the outside-root
and traversal 403s, the absent 404, the file 400, the 405; the static
assets carry the forge, the browser, the tool-block renderer, the
class-hidden toggle, and the effort ramp.

### 15. The todo's two hands.

The operator can start, complete, and retry a task from the page: `POST
/api/todo/start {id}`, `POST /api/todo/complete {id}`, and `POST
/api/todo/retry {id}` call `todo.Start`, `todo.Complete`, and `todo.Retry` with session `dashboard` — the same
verbs the tool calls, the same walls as every write (POST, Origin, the
body cap), the id checked to the tool's shape (`tN`). The reply is the
store's voice, the queue re-read after; a task the model claimed
refuses as it would for any foreign session (fail it first to take
over), and the page shows that refusal, never works around it. Named
non-goals: fail from the page (the operator's judgment calls,
the TUI's), reordering (move), editing a task's text (the event log has
no such event). Cases, failing first: start marks the seeded task
active and complete marks it done (the read carries `[~]` then `[x]`);
an unknown id is the verb's refusal; a malformed id is a 400; the
Origin wall holds; a GET on the verb is a 405.
