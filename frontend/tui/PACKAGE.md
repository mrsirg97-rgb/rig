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

- **The Frontend shell** (`tui.go`): the reader goroutine, the
  command dispatch, the completion menu, the status line's refresh
  points, the `Steerer` seam (`Steer`, `Interrupt`, `ClearSlot`,
  `LiveTurn`, `Ask`), and the live-region protocol.
- **The live region** (`live.go`): the activity, pending, menu, input,
  and status rows; cursor-up redraw, width handling, the one-op-one-write
  frame (the write gate, decision 2).
- **Committed blocks** (`commit.go`): turn text, reasoning, tool rows
  (the result body head/tail; write and edit preview their arguments
  first; the content, the `-`/`+` sides; decision 4 amended), command
  output, the usage, the compact line, the fault line.
- **The status line and startup block** (`status.go`): the live row
  (`RenderStatusLine`, three rows; identity / effort · role · approve ·
  workers / usage; the stance row always ends with the fleet's model or
  the one-shot startup block (`RenderStatus`: the title, the session, the
  fleet as `workers: <model|none>`, and the hint line), and the
  snapshot's refresh points (start, `/new`, `sessions resume`, a
  `models` switch).
- **The tool and scheduler renderers** (`tools_render.go`): one renderer,
  both doors; the tool-result path and the command path commit
  byte-equal blocks minus the opening line (decision 6).
- **Input** (`input.go`): raw mode, the key parser (Tab, Shift-Tab as
  CSI Z, arrows), single-line editing with history, bracketed paste, and
  the completion menu's state.
- **Themes** (`theme.go`): the four shipped palettes (`oled`, `paper`,
  `p1`, `p3`), the glyph table (`unicode`, `ascii`), the effort ramp's
  slots, `theme.json` schema and merge, the 256 downconvert.
- **Escape helpers** (`ansi.go`), the markdown pass (`markdown.go`),
  the pager (`pager.go`), width and wrap (`width.go`, `wrap.go`).

## How it is consumed

- The root wires it with `New` plus options: `WithTheme`, `WithWidth`,
  `WithStatus` (the status numbers computed at the refresh points, a
  store read never per repaint), `WithNews` (the scheduler's one ambient
  line), `WithCommands` (the dispatch + the `Steer` seam + the `Sub()`
  hint door), `WithTicks`/`WithWinch` (test seams).
- `Input` returns one user message (the loop's contract): the steering
  slot is delivered before blocking, a command line is dispatched and
  consumed there, blank lines are no-ops, EOF ends the REPL.
- `Notify` observes the stream events and renders at the commit points
  exactly: deltas as they arrive, the tool block on `ToolResult`, the
  newline guarantee on `Done`, the fault line, the compact line, the
  usage on `TurnEnd`. Events it does not name are ignored (the compat
  rule).
- The `Steerer` is the frontend-owned seam (SPEC_COMMANDS 2): `Steer`
  queues text and reports the interrupt; `LiveTurn` is the turn's
  state; `Ask` is the approval gate's door (SPEC_MODES 4).
- `-tui` on the root defaults to auto: the TUI when stdout is a
  terminal, the plain CLI when piped. The CLI stays the piped reference;
  the TUI adds, never changes, the CLI's bytes.

## Gotchas

- The commit points are the events, exactly: unknown events are ignored,
  never misread.
- The TUI's one departure from the CLI's bytes is the spacing rule
  (decision 2): the transcript never carries two blank rows, and a
  reasoning or tool block's close gets exactly one blank row before what
  follows. The CLI keeps every byte.
- The size is read at the repaint, not the signal (the two-client tmux
  race): a repaint between the resize and the SIGWINCH must not use a
  stale width.
- One op is one write (the write gate): a repaint's escapes and rows
  flush as a single write, so no partial frame and no row left ending
  exactly at the last column across a write boundary (the tear). The
  pager's entry is one write too; the alternate-screen switch and the
  first frame together; written apart, a reader (the copy-mode case,
  under `-race` on CI) could see the switch before the history.
- Tabs expand at ingestion (runewidth gives a tab width zero: the
  terminal advances to an 8-column stop), or the pending line's row math
  breaks and every repaint leaves a copy.
- A live turn steers, established or not (decision 9, amended twice): a
  line typed during a live turn steers; a paste is one input (bracketed
  paste retired the first-event gate's reason).
- Esc's ladder: pager, then menu, then the prompt clear: and on an
  EMPTY prompt during a live turn, the interrupt (stopping is not saying
  something).
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
