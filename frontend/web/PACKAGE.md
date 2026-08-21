# frontend/web

## What it is

The local dashboard (SPEC_SERVE): a loopback-only `net/http` server that
reads the rig home's stores and renders them as one embedded page. It is
a *reader* of the same SQLite files a live session is writing — the same
rig home, the same pragmas, the same store verbs — and it has exactly one
write (a todo create, attributed to `dashboard`). It is a leaf package
wired once at the root, beside `cli`/`tui`; the loop never names it. No
framework, no build step, no external asset: `go build` alone ships it.

## What it includes

- **`Server` / `New` / `ListenAndServe` / `Handler` / `Close`** — the
  dashboard: the static inputs (the rig home, the serve cwd, the models
  table, the crontab), the token, the allowed origins, the store cache,
  the rooted static assets.
- **The token gate** (SPEC_SERVE 5) — `serve.token` (0600) minted once and
  printed once; `Authorization: Bearer`, the one-time `?token=` (which then
  sets the cookie), or the cookie; a constant-time compare; a 401 with the
  `WWW-Authenticate: Bearer` challenge.
- **The loopback refusal** — `-addr` must name a loopback interface
  (`127.0.0.1`, `::1`, or `localhost`); a non-loopback bind is refused by
  name before the listener opens.
- **The allow-list router** — every (method, path) is named; an unknown
  path is a 404 and a known path with the wrong method is a 405 (with the
  `Allow` header); the static assets are the only non-API surface.
- **The read views** — sessions (the list, grouped by workspace, and the
  transcript as structure: messages, reasoning, tool calls and results,
  and the usage rows), todo and scheduler (the store's own text, verbatim),
  memory (recent for the cwd), models (every row: window, effort list,
  role), plugins (the loaded set and the pending zone, each with the file's
  DESCRIPTION).
- **The one write** — a todo create (one task per line): Origin-checked
  (same-origin only), body-capped, calling `todo.Create` with session
  `dashboard`, the reply verbatim. The only `db.Tx` (not `TxReadOnly`) the
  dashboard opens.
- **The store cache** — opens the store files the way the root does (the
  stores' own path helpers, one shared source) and caches them per file;
  a reader of the same files, never a second copy.
- **The static assets** (`static/`) — the single page, the sidebar nav,
  the cwd picker, the create form, and the oled palette's values
  (the `frontend/tui` `theme.go` table).

## How it is consumed

- The root wires it with `web.New(web.Options{Home, CWD, Models, Crontab})`
  and `srv.ListenAndServe(ctx, addr)`, behind the `serve` subcommand
  (`cmd/rig/serve.go`, one file plus a registration line in `main.go`).
- The models table is `cfg.Models` (the config's table, every row); the
  crontab is `sched.RealCrontab("")` (injected in tests).
- The operator opens `http://127.0.0.1:7777/?token=<printed>` once (the
  cookie is set); the same-origin fetches then carry it.

## The seams it uses, verbatim

The dashboard calls store verbs, never domain accessors and never raw SQL
(SPEC_SERVE 3): `state.ListSessions`, `state.Resume`,
`state.SessionUsage`, `todo.Read`, `todo.ReadAll`, `todo.Create`,
`scheduler.List`, `rem.Recent`, and the `models.Table` / `plugins.List`
surfaces. The two small store additions (`SessionRow.Cwd`,
`SessionUsage`) are typed verbs the dashboard consumes, not a SQL leak.

## Gotchas

- The per-cwd path formulas are the stores' own: `state.StorePath`,
  `todo.StorePath`, `rem.FilePath`, and the scheduler's `StorePathFor`
  under `<home>/scheduler` (the root's home, not the rig home directly).
  The root's inline formulas were replaced by these helpers (one source).
- `todo.Create` is an upsert (it adds the given tasks and keeps the
  existing queue); the reply is the store's voice, shown verbatim. The
  dashboard does not assert a replace — it shows what the verb says.
- The sessions store defines a workspace: a read of a cwd with no state
  file is a named 404 (fail closed); the todo/memory/scheduler reads open
  (creating if absent) and show the store's empty voice.
- The transcript is paginated (`limit`/`offset`) over `Resume`'s messages;
  `Resume` itself loads the full transcript, so a very long session loads
  fully before the page is sliced (phase 1's bound).
- The token is minted on first use and printed once (stderr); a second
  open reads it, never re-mints. The token never rides a response body.
- The write's Origin must name the bound address (and `localhost` when the
  bind is loopback); the origins are set from the bound address at serve
  time (set directly in tests).

## Tests

The named cases (SPEC_SERVE, testing), `httptest` over a temp rig home
with seeded stores (the store path helpers are the round-trip): the token
gate (401s, the bearer, the `?token=` cookie, the 0600 file, no re-mint);
the loopback refusal (accepts and refuses by name); the allow-list (404,
405 with `Allow`); the write's walls (Origin, body cap, empty body, the
verbatim reply, the upsert); the reads (the cwds, the sessions list and
cwd, the transcript golden — messages, reasoning, tool calls and results,
and the usage rows — the todo/scheduler verbatim text, the memory, the
models rows with effort and role, the plugins with DESCRIPTION); and the
static assets (served, 404, traversal refused). The store additions carry
their own cases in `store/{state,todo,rem}`.
