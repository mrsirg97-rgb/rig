# frontend/tui

## What it is

The terminal Frontend (SPEC_TUI): the same events, the same commands,
the same tools as the CLI, rendered in pane's design language with
fewer parts. Scrollback-native (decision 1): the terminal owns
history; rig decorates a small live region at the bottom; the menu,
the activity row, the pending line, the input, and the status row
(decision 2). It implements `core.Frontend`, dispatches `core.Command`,
and adds two leaf deps (`x/term` raw mode and size, `go-runewidth`
width); no core or loop line (decision 10).

## What it includes

- **The Frontend shell** (`shell.go`): the reader goroutine and the
  constructor; `keys.go` and `prompt.go`: the key handling and the
  input seam; `notify.go`: the event rendering and the live-region
  protocol; `commands.go`: the command dispatch and the `Steerer` seam
  (`Steer`, `Interrupt`, `ClearSlot`, `LiveTurn`, `Ask`); `menu.go`:
  the completion menu; `paint.go`: the flow and the paint seam;
  `frame.go`: the repaint cadence.
- **The live region** (`live.go`): the activity, pending, menu, input,
  and status rows; cursor-up redraw, width handling, the one-op-one-write
  frame (the write gate, decision 2).
- **Committed blocks** (`commit.go`): turn text, reasoning, tool rows
  (the result body head/tail in screen rows at the terminal's width,
  2.11.11, so a one-line blob elides like a thousand short lines; write
  and edit preview their arguments
  first; the content, the `-`/`+` sides; decision 4 amended), command
  output, the usage, the compact line, the fault line.
- **The status line and startup block** (`status.go`): the live row
  (`RenderStatusLine`, three rows; identity / effort · role · approve ·
  workers / usage; the stance row always ends with the fleet's model or
  the one-shot startup block (`RenderStatus`: the title, the session, the
  fleet as `workers: <model|none>`, and the hint line; `WithTitle`
  replaces the rig block letters, adds a tagline, and names the ascii
  fallback; `StatusIn.Rows` is the embedder's footer band under the same
  dim rule the band row uses, nothing when empty), and the
  snapshot's recapture: every successful command re-reads the status
  function (the Used reset stays at `/new` and `sessions resume`), so
  the embedder's rows follow their commands, not a name list; with
  `WithStatusTick` the Input loop re-reads it every d while idle, and
  the region redraws only when the rows changed (zero is off, and
  rig's own main never sets it)).
- **The band row** (`band.go`, 3.2.0): everything happening in the
  background passes through one row under the status rows, one item at
  a time, in the order it happened — a delegate's or a swarm's start,
  a scheduled job's start and end, a worker's tool call, and every
  `evt.Notice`. The row never goes away: a dim rule (four cells) over
  one row, the same height idle or busy. Items come in two kinds:
  events never drop and keep their order (starts, ends, failures —
  including the room's own end for a runner that died without posting
  one, `failed · runner left` — and the stores' notices, identical ones
  collapsing), and status items
  replace — a worker's newer tool call replaces that worker's call
  still waiting in the queue, so the backlog is at most one call per
  worker however fast calls arrive. The row shows the breathing item
  (`BreathPaint` on the item's slot: a call in the warn slot, a notice
  in its level's slot, a start and a done in the text and success
  slots, a failure in the fault slot), then holds it dimmed until the
  next one, `—` before the first. Each breath is `emberBreathStops`
  (12) × `animPeriod` (120 ms) of the ember's curve, riding the one
  frame ticker; the breath runs whether or not a turn, a compaction or
  an aside is live, and the ticker stops once the queue rests and the
  row holds. A call is `#<worker> <call> · <age>`, prefixed with the
  job's id for a scheduled job's worker (`j8 #1 …`), the call cut at
  `bandCallCols` (25) display columns with `…` on rune and
  display-width boundaries — 25 is the call width the 52-column phone
  mockup shows, which leaves the row `j8 #1 ` + 25 + ` · 2s` = 36
  columns at the phone's 52. The ages and the durations are computed
  once, at the moment the item was made: the row is a sequence of
  snapshots, not a live read.
- **Phases share the row too** (`frame.go`, `phasepreview.go`, 2.11.7):
  a `core.Phase` opening takes the indicator's row as `<name> · <elapsed>`
  on the ember when no turn owns it. Since 2.12.9 its deltas stream as a
  preview under that row, never into the record: a rolling tail of at
  most `phasePreviewRows` (10) screen rows, dim reasoning, headed by
  `· n rows above ·` once the tail scrolls, gated by the same reasoning
  toggle and re-measured on resize. With the end the preview is gone —
  nothing of it reaches the scrollback. Then the row goes idle; the
  band row's breath never waited on the phase. A phase that arrives
  during a live turn waits
  and its deltas are not shown (the run log has them).
  One phase is open at a time and it is ended, never abandoned:
  `reviewing` ends on its settle, committing the check line (a green
  check or a red cross beside the name and its note). `summarizing` has
  no check line — the compaction line is its end — and since 2.13.1
  `Compacted` and `Fault` close the phase through the same door
  (`endPhaseLocked`), because a reactive compaction runs inside the turn,
  where the cue cannot take the row and an abandoned phase would keep the
  indicator lit and the ticker spinning for the rest of the session.
  Phases are rendered, not stored: the recorder has no case for one, and
  the web feed carries them live. A notice takes the band row (above),
  never the indicator's: the indicator row shows only the turn, the
  compaction and the phase.
- **The publishers** (`swarm.go`, `notify.go`, 3.2.0): a `SwarmStatus`
  no longer renders as a band; it feeds the row. A snapshot is told
  apart by the role it stamps (`delegate`), since a `SwarmStatus`
  carries no origin — the delegate's rows route to the delegate's slot,
  the rest to the swarm's, a zero-row status is its publisher's
  "nothing running" and clears both (2.14.7). The transition from
  nothing running to workers running makes the start event
  (`delegating · N worker(s)`, `swarm · N worker(s)`); each running
  worker's tool is its own status item keyed by its publisher and id,
  so a delegate's worker and a swarm's worker never replace each other.
  The old band's per-role counts and its busiest tail are the swarm
  tool's story now (`SPEC_SWARM 7`); the row carries one item at a
  time.
- **The worker inbox** (`worker.go`, 2.14.0): `core.WorkerDone` events
  append to an inbox and wake `Input`; the inbox drains at the top of
  `Input`, ahead of the steer slot, as one block in arrival order, and a
  drain that happens with no live turn starts a turn of its own. A live
  turn is never interrupted. The block is `core.WorkerBlock`, painted as
  the turn's line because from there it is what the model was told.
- **The tool and scheduler renderers** (`tools_render.go`): one renderer,
  both doors; the tool-result path and the command path commit
  byte-equal blocks minus the opening line (decision 6).
- **The reply renderer** (`list_render.go`): `RenderReplyBlock` is the
  frame every other command reply gets since 2.9.1: the opening, then
  the lines; an ack's `name: ` prefix is dropped and the ack reads dim,
  the rest is text. Refusals paint in the error slot under the same
  opening.
- **The list renderer** (`list_render.go`): `RenderListBlock` paints
  every other command reply in the list shape (SPEC_COMMANDS 13): the
  opening, the head dim, each row's marker as the todo glyph, the id
  dim, the first segment in text and the ` · ` details dim, the `· n
  more` footer dim; a reply with no id rows is left to the plain text
  path. `WithCommands` also hands the Env its row budget
  (`Env.Lines`: the height minus the status rows, the opening and the
  input row).
- **Input** (`input.go`): raw mode, the key parser (Tab, Shift-Tab as
  CSI Z, arrows), single-line editing with history, bracketed paste, and
  the completion menu's state.
- **Themes** (`theme.go`): the six shipped palettes (`warm`, `cool`,
  `paper`, `p1`, `p3`; `oled` the legacy alias of `warm`), the glyph
  table (`unicode`, `ascii`), the effort ramp's slots, `theme.json`
  schema and merge, the 256 downconvert, and the one-dial resolution:
  a set settings key names the theme alone (`custom` names the file,
  which must exist); with no key the file is the theme when present,
  else `warm`.
- **Repaint** (`repaint.go`): the optional `RepaintTheme(tui.Theme)`
  seam `/theme`'s root pushes through — the live swap of `t.theme`;
  the dispatcher's post-command redraw paints the status and the live
  region in the new theme, and committed scrollback keeps its bytes.
- **Escape helpers** (`ansi.go`), the markdown pass (`markdown.go`),
  the pager (`pager.go`), width and wrap (`width.go`, `wrap.go`).

## How it is consumed

- The root wires it with `New`: the resolved theme is the third argument
  (the root reads theme.json; the frontend never resolves one), then
  options: `WithWidth`, `WithStatus` (the status numbers computed at the
  refresh points, a store read never per repaint), `WithStatusTick`
  (the idle recapture: while the Input loop waits for a line, the status
  function is re-read every d and the region redraws only when the rows
  changed; zero is off and rig's own main never sets it), `WithNews`
  (the scheduler's one ambient line), `WithCommands` (the dispatch + the
  `Steer` seam + the `Sub()` hint door), `WithTitle` (the welcome
  block's plain name, title rows, and tagline; default the rig rows
  with none), `WithTicks`/`WithWinch` (test seams).
- `Input` returns one user message (the loop's contract): the steering
  slot is delivered before blocking, a command line is dispatched and
  consumed there, blank lines are no-ops, EOF ends the REPL.
- `Notify` observes the stream events and renders at the commit points
  exactly: deltas as they arrive, the tool block on `ToolResult` (the
  separating blank flowed there when the last committed thing was a
  tool block, the start's newline kept only after text or reasoning),
  the newline guarantee on `Done`, the fault line, the compact line, the
  empty-turn notice (`RenderEmptyTurn`, its usage added to the turn
  totals), the usage on `TurnEnd`. Events it does not name are ignored
  (the compat rule). `toolStarts` keys a wave's in-flight calls by ID,
  kept by call ID until the result consumes it, so a result block
  renders the call that produced it, not the wave's latest start.
- The `Steerer` is the frontend-owned seam (SPEC_COMMANDS 2): `Steer`
  queues text and reports the interrupt; `LiveTurn` is the turn's
  state; `Ask` is the approval gate's door (SPEC_MODES 4).
- `-tui` on the root defaults to auto: the TUI when stdout is a
  terminal, the plain CLI when piped. The CLI stays the piped reference;
  the TUI adds, never changes, the CLI's bytes.

## Gotchas

- **Text from outside is printed, never obeyed** (`printable.go`, 2.18.3):
  a tool's result, the strings of its call, the model's text, a worker's
  task and tool, a notice — each passes `printable` before it is laid
  out, which drops every escape sequence and control character except
  the newline and the tab. An editor that starts inside a bash call
  writes the alternate screen and a scroll region to its pipe; printed
  raw, the scroll region outlives the block and every later redraw
  overwrites the transcript. A new door for outside text takes the same
  call. The model still reads the bytes the tool produced.
- The commit points are the events, exactly: unknown events are ignored,
  never misread.
- The TUI's one departure from the CLI's bytes is the spacing rule
  (decision 2): the transcript never carries two blank rows, and a
  reasoning or tool block's close gets exactly one blank row before what
  follows, and a tool block gets one before it when another block
  precedes it — the separating blank is the result's, flowed on
  `ToolResult`, the start's newline kept only after text or reasoning,
  so a parallel wave renders as the alternating one does. The turn's end gets the same guarantee: the last committed
  row and the input line stand one blank apart, whether or not the reply
  ended with a newline (`TurnEnd` commits the blank after the pending
  text drains; `live.draw`'s blank merge keeps a double out). The CLI
  keeps every byte.
- The winch handler follows the input (SPEC_TUI, the 1.2.1
  amendment): a terminal input owns the SIGWINCH handler whether or
  not it is fd 0 — stdin is fd 0, and the guard that keyed on a
  nonzero fd left the terminal on stdin with no handler at all, so a
  resize with nothing streaming never repainted. The handler's full
  draw is the winch re-layout: size read, region repaint from the
  parked aim.
- The size is read at the repaint, not the signal (the two-client tmux
  race): a repaint between the resize and the SIGWINCH must not use a
  stale width. The height rides beside it: the live region is bounded
  by the viewport (SPEC_TUI, the 1.1.0 amendment), because a region
  taller than the pane repaints with the cursor-up clamped at the
  screen's top and writes itself over committed history. The pending
  prose line yields first (the whole line wraps at words every frame
  and its tail is the last wrapped rows under the `· k lines hidden ·`
  marker, so a laid row never changes while it stays visible), then
  the menu's window, then the input's five-row window.
- The stability check's status offset is the status block's real row
  count (the blank above plus the rendered rows), not a constant: the
  check decides between the in-place input-row edit and the full
  re-layout, and a wrong offset turns every keystroke into the heavy
  path. It also refuses the in-place edit while the painted width is
  stale (SPEC_TUI, the 1.1.1 amendment): the keystroke takes the full
  re-layout, which aims with the painted geometry.
- The aim is painted geometry: the region keeps the row count and
  width of its last paint, and every cursor-up aims with that count,
  capped at the viewport. Measuring the old region in the new
  geometry — the size changed between paints — overshoots the true
  top and writes the region over committed history. The status
  block's viewport budget counts its wrapped rows, not its logical
  ones, so the bound holds on a pane narrow enough to wrap the usage
  line.
- A submit aims at the top of the live block above the input: the
  verb menu's rows repaint away with the echo, the separator blank
  survives (a blank first row of the region can only be that
  separator), and a submit on a live turn carries the activity row
  into the new region — the turn owns it.
- Committed bytes expand tabs on the paint seam (`live.draw`): a tab
  advances to the next eight-column stop while the width math counts
  it as nothing, so tab-indented tool output rendered wider than the
  bookkeeping saw and every row after the first tab drifted. SGR
  sequences copy through at zero width; the flow path's expansion
  already covered the model's text.
- The aim caps at the viewport: the size is read at the repaint, and
  a height-only shrink (the phone's keyboard) cuts the pane under a
  region painted for a taller one — the first cursor-up after the
  shrink holds inside the pane the repaint finds.
- A capped aim marks the region (`capped`): the terminal clamped the
  cursor at the viewport's top, so rows of the taller paint were left
  standing above it and the app's idea of where it painted diverged
  from the screen's. A phone terminal brings those rows back into view
  when it grows (the keyboard closes); no arithmetic can place them.
  The next repaint after a capped aim is a viewport reset: cursor-up
  by a full pane (row one wherever the cursor stands), clear below,
  paint the region from the top. The transcript rows that pane still
  showed are the price; they live on in the scrollback. The mark
  clears on the reset and on a submit.
- The aim starts from the park (SPEC_TUI, the 1.1.3 amendment): the
  keystroke fast path parks the caret `parked` rows above the
  region's bottom, and the repaint never re-anchors through a
  cursor-down — its cursor-up is the region's uncapped row count
  minus one minus `parked`, and only the result is capped at the
  viewport (the cap belongs to the aim, not to the span: a pane that
  shrank under a region painted for a taller one must aim all the way
  to the region's top), and the park clears after the paint. tmux
  deletes rows below the cursor before scrolling the top into
  history, so a shrink that lands while parked makes the re-anchor a
  no-op and the following cursor-up would overshoot by `parked`,
  writing the region over committed rows. The submit, the winch
  re-layout, and the in-place input edit (its cursor-up measured from
  the parked row) apply the same; the caret still rests on the input
  row after an in-place edit.
- The pager steps by the frame, not the height (SPEC_TUI, the 1.1.2
  amendment): PgUp advances the offset by the lines the current frame
  actually showed, minus one; PgDn walks forward from the frame's
  bottom line, accumulating `rows()` against the same row budget, and
  steps by that many minus one. The offset stays in lines and the
  pages overlap by one line, so a record holding lines wider than the
  pane — tool results and code blocks commit unwrapped — shows every
  line across the pages.
- An underscore is intraword: `_` opens or closes emphasis only when
  the neighboring character is not a letter or digit (the CommonMark
  rule), so snake_case identifiers keep their underscores; `*` keeps
  the simpler rule.
- The freeze gate is a CI job now (`go run ./cmd/freeze`, the freeze job
  in `.github/workflows/ci.yml`): the allowlist is `specs/FREEZE.txt`, one
  path per line, and a reopening is a one-line diff to that file, reviewed
  in the PR. The reopenings are named there with their versions: `loop`
  (1.1.4, the fed-back error line), the 2.0.1 module-path rename (the
  root and core files whose import lines move to the /v2 path), and
  `core/provider.go` (2.8.3, the reviewer on the resident model).
- One op is one write (the write gate): a repaint's escapes and rows
  flush as a single write, so no partial frame and no row left ending
  exactly at the last column across a write boundary (the tear). A frame
  writes over the old region — the row's replacement content lands
  first and the tail is erased after (`K`, the shrink below with `0J`) —
  so a reader that splits the write at the pty buffer (tmux reads in
  chunks; a large commit can split) never sees the region erased and
  blank: it sees the old frame, then the new one in place. The repaint
  is wrapped in the synchronized-output mode (`?2026h`/`?2026l`, one
  write, pair included): terminals that support it buffer the pair and
  paint once, so the write boundary is never visible at all. tmux's
  pane-side support for the pair landed in 3.7 — 3.4 consumes the
  sequences without forwarding them, so under it the write-first
  protocol is the whole protection, and the pair is inert. The pager's
  entry is one write too; the alternate-screen switch and the first
  frame together; written apart, a reader (the copy-mode case, under
  `-race` on CI) could see the switch before the history.
- Deltas paint on a 16 ms frame cadence (`flow` marks the region dirty,
  the tick paints once per frame): tokens that arrive together repaint
  together, and the commit points stay immediate. The activity breath
  keeps its own 120 ms pace on top. The ticker exists only while a turn
  or a compaction can paint: it starts with `startTurnLocked` and with
  the `Compacting` event, and stops once the turn's final commit or the
  compaction has drained (an idle TUI wakes nothing). `busyLocked` is
  what the ticker breathes for, and it is a turn, a compaction, a
  notice, an aside, or a delegate batch — the batch keeps running with
  no turn at all, and its row must not freeze where the operator is
  looking.
- The status tick is the idle complement (`WithStatusTick`): the status
  function is re-read every d on the Input loop while no turn streams
  and no compaction runs, and the region redraws only when the rows
  changed. The recapture lives on the Input loop, which a turn's start
  leaves, so the tick never fires mid-turn; a buffered tick lands
  immediately when the next Input starts.
- The pending paragraph wraps incrementally (SPEC_TUI, the 1.2.16
  amendment): greedy word wrap is prefix stable, so `pendWrap` caches
  the wrapped rows plus the cells that begin the last row and folds
  each delta into the last row alone; a rebuild happens only on a
  width change or an edit that is not an append. `liveRegionLocked`
  starts the pending block at the viewport height and the budget loop
  slices the cached rows to the cap, and the block's row count is its
  row length (each wrapped row is one visual row by construction), so
  `rowsOver` never measures rows that will not be painted. The rows
  stay byte-identical to `wrapSegs(theme, width, t.pend)` at every
  step, exact-width rows and the trailing-space trim included; the
  space a soft break skips still charges its width to the row it left,
  and the fold carries that in its start column.
- Tabs expand at ingestion (runewidth gives a tab width zero: the
  terminal advances to an 8-column stop), or the pending line's row math
  breaks and every repaint leaves a copy.
- A live turn steers, established or not (decision 9, amended twice): a
  line typed during a live turn steers; a paste is one input (bracketed
  paste retired the first-event gate's reason).
- Esc's ladder: pager, then menu, then the prompt clear: and on an
  EMPTY prompt during a live turn, the interrupt (stopping is not saying
  something). With no turn to interrupt the gesture is the idle
  interrupt (`WithIdleInterrupt`): an esc with an empty line stops the
  running workers — a session without a delegate passes nothing, and
  the gesture keeps clearing the line.
- The completion menu is the operator's help: two or more candidates
  show the menu, one shows the ghost, Enter over navigation accepts the
  pick, without navigation the typed line dispatches.
- Reasoning is never decorated (11, amended): its fences render raw grey;
  a thought's unclosed fence never leaks into the next turn (the
  miscolored-session bug, fixed by removing the lexical highlighter).
- The approval ask (SPEC_MODES 4) is the screen's one modal: while the
  question stands, y approves, n declines, Esc declines and interrupts,
  every other key is swallowed; a nil channel (the ask resolved by the
  context) is a no-op.
- The status row is the region's last row: a wrapping usage row on a
  narrow terminal counts by its terminal rows, like every live row.
- The golden stream stamps its delegate heartbeats hours old, so the
  band's age cells read the same on every run.
- The scripted session's helpers carry their seams: `inputWhile` feeds
  a line to a reader that has not started yet (the TUI's reader belongs
  to Input, so the keystroke has to follow the call, not precede it),
  `inputAt` runs Input on the caller's context (a steer cancels the
  turn it interrupts, and a test must not inherit that), and `since` is
  what has been painted after the `tail` mark — the stream is
  append-only, so "left the screen" only means anything about the
  frames after the mark.
