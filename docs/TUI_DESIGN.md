# TUI implementation notes

`specs/SPEC_TUI.md` is the design; the decisions below are the parts the
spec leaves to implementation. Each is pinned here because the golden
tests assert on the exact bytes.

## the pure core / shell split

Every renderer is a pure function: state in, bytes out, no I/O. `theme.go`
(palette and glyph tables, theme.json decode, the 256 downconvert),
`status.go`, `commit.go`, and `tools_render.go` hold the renderers.
`live.go` turns (old live lines, committed chunk, new live lines) into the
escape stream. `input.go` is the key parser and line editor over a byte
stream. `tui.go` is the shell: the Frontend, the reader goroutine, the
command dispatch, the completion menu, and the status line's refresh
points. Tests drive the pure core directly for the goldens and the TUI for
the protocol; `tear_test.go` pins the live-region tear from three doors.

## the live region protocol

Invariant: everything above the live region is committed and is never
touched again. The live region is the last written rows, top to bottom:
the completion menu's rows when one is open (decision 9), the activity
line and the pending prose line during a turn, the input line (itself up
to five terminal rows as it wraps, a five-row window that follows the
cursor beyond that), and the status line (decision 3) which is always the
region's last row. The terminal cursor is parked on the region's last row
(the status row's, or the input row's when none stands there) after every
op, or at the edit column of a wrapped input (the `parked` tally,
un-parked by a cursor-down, the `norm` step, before any other op's
arithmetic).

To commit a chunk and redraw: clear the old region's terminal rows (the
count is the rows the old lines wrapped to, `visualRows`), cursor up to
its top, write the committed chunk, then the new live lines. Committed
bytes are never rewritten. The only cursor arithmetic is up, down,
set-column, and clear-line, over at most the cap (decision 2's amended
at-most-three).

The region is bounded by the viewport height (SPEC_TUI, the 1.1.0
amendment), read at every repaint beside the width. An over-tall
region cannot be repainted cursor-relatively: the terminal clamps the
cursor-up at the screen's top and the rewrite lands over committed
text, leaving rows the bookkeeping can never clear. The pending prose
line therefore wraps at words incrementally (the SPEC_TUI 1.2.16
amendment: appending text rewraps only the last row, and the row
count is the wrap's own row length) and renders its last
wrapped rows under the dim `· k lines hidden ·` marker (k is the
wrapped total minus the visible tail), the menu's window shrinks
next, and the input's five-row window shrinks last; the shrink order
keeps the operator's controls alive as the room runs out. The tail is
the last wrapped rows, never a column slice: a laid row stays as laid
while it is visible, only the last row grows with a delta, and the
block scrolls by exactly one row when a new row starts. A keystroke
on an otherwise stable region rewrites the input row alone: the
stability check counts the status block's real rows (the blank above
plus the rendered rows), which is what decides between the in-place
edit and the full re-layout.

The aim is painted geometry (SPEC_TUI, the 1.1.1 amendment): the
region remembers the row count and the width it was last painted at,
and every cursor-up aims with that count, capped at the viewport — a
paint that overflowed the pane scrolled its own head into history, so
the painted span is what the next aim must clear. Measuring the old
region in the geometry about to be painted (the size is read at the
repaint, so a resize re-measures mid-stream) overshoots the true top
and writes the region over committed history. The stability check
refuses the in-place input-row edit while the painted width is stale;
the keystroke takes the full re-layout, which aims correctly by
construction. A submit aims at the top of the live block above the
input: the menu's rows repaint away, the separator blank between the
transcript and the region survives, and a submit on a live turn
carries the activity row into the new region, because the turn still
owns it. The invariant extends to the bytes: committed bytes expand
tabs on the paint seam — a tab advances to the next eight-column stop
while the width math counts it as nothing, and a row painted with raw
tabs wraps into rows the bookkeeping never sees. It extends to the
pane too: the size is read at the repaint, and a height-only shrink
(the phone's keyboard) cuts the pane under a region painted for a
taller one, so the first cursor-up after a shrink caps at the pane
the repaint finds. The aim starts from the park (SPEC_TUI, the 1.1.3
amendment): the keystroke fast path leaves the caret parked `parked`
rows above the region's bottom, and tmux's shrink deletes rows below
the cursor before scrolling the top into history — the cursor stays
put — so a shrink that lands while parked makes a cursor-down
re-anchor a no-op and the following cursor-up overshoots by
`parked`. The repaint never re-anchors: its cursor-up is the
region's uncapped row count minus one minus `parked`, and only the
result is capped at the viewport — a pane that shrank under a
region painted for a taller one must aim all the way to the region's
top, not to the shrunken viewport's; the park clears after the paint.
The submit, the winch re-layout, and the in-place input edit (whose
cursor-up is measured from the parked row) apply the same. The caret
still rests on the input row after an in-place edit. The winch signal
itself follows the input (SPEC_TUI, the 1.2.1 amendment): a terminal
input owns the handler whether or not it is fd 0 — stdin is fd 0,
and the guard that keyed on a nonzero fd left a terminal on stdin
with no SIGWINCH at all, so a resize with nothing streaming never
repainted the status rows the shrink deleted below the parked caret.
Single-line edits (typing, the spinner tick) clear and
rewrite the input or activity line in place; a shape change (the menu
opens, closes, or moves) re-lays the whole region (`editFull`).

The wrap model is the deferred one (xterm's, the common case): a
character written at the last column stays on the row until the next write
or cursor move, and the protocol's `lineEnd` (`toCol(1)` then LF) is built
for it. Each op is one write (the write gate, `live.go`): its escapes and
rows buffer and flush as a single write, so the terminal sees every
repaint whole and no row ends exactly at the last column across a write
boundary: the one whose pending wrap a terminal may resolve at the
flush, shifting the cursor a row and taking the next op's cursor tally off by
a row (the tear, `tear_test.go`).

## the pager

PgUp/PgDn step by lines, but the step comes from the frame, not the
height (SPEC_TUI, the 1.1.2 amendment): PgUp advances the offset by
the lines the current frame actually showed, minus one; PgDn walks
forward from the frame's bottom line, accumulating `rows()` against
the same row budget, and steps by that many minus one. The offset
stays in lines and the pages overlap by one line, so a record holding
lines wider than the pane shows every line across the pages. The
arrows still step one line, Home/End jump, and the frame renders the
tail of the record above the offset that fits the row budget.

## the event map

The commit points are the events, exactly (SPEC_TUI decision 2):
ReasoningDelta and TextDelta stream as they arrive (reasoning dim and
only while the toggle is on), ToolStart switches the activity line to the
tool name, ToolResult commits the whole tool block (the separating blank
flows here when another block precedes it), Done guarantees a
trailing newline and the status line's used takes its Usage, TurnEnd
commits nothing but closes the pending flow and resets the live region to
the input line,
Compacted commits the compact line and the status line's used takes the
compact's Kept (no block reprint), Fault commits the fault line, unknown
events are ignored. The activity label is the phase: thinking before and
between tools, the tool name while one runs. A `Phase` opens the
indicator row and owns it: the phase's deltas buffer and the live region
draws them as a preview — a rolling tail of at most ten screen rows, dim
in the reasoning slot, headed by one dim `· n rows above ·` line once
the tail scrolls; the end commits only its check line (for `summarizing`,
the `⧉ compact` line after it), the preview gone with the phase's last
frame, so a phase streams a peek, never a transcript. `WorkerDone`
commits nothing: it lands in the inbox and wakes `Input`, which drains
it ahead of the steer slot as the head of the next turn's user message;
returns are not coalesced — two in one turn arrive in the order they
finished. The spinner is state (a frame
index), not time; a ticker goroutine advances it, and tests pin the frame.

## the status line and news seams

The TUI renders; the root computes. `tui.WithStatus(fn)` supplies the
status line's and the startup block's numbers (model, effort, window,
session up/down/cache): one committed startup block at session start
(decision 3, the banner's identity and session rows without the dotted
rules), and the live status row's snapshot at the refresh
points (session start, `/new`, `sessions resume`, and a `models` switch),
never per repaint (the closure is a store read; a live row repaints on every
keystroke). The used number is the frontend's own arithmetic over the
usage events (the last Done's Prompt+Completion, then the compact's
Kept); `new` and `resume` reset it with the session.
`tui.WithTitle(name, rows, tagline)` replaces the welcome block's rig
block letters with the embedder's rows and adds a tagline under them
(default: the rig rows, no tagline); the ascii glyph fallback prints
the plain name.
`StatusIn.Rows` is the embedder's footer band: the rows render under the
status line behind a dim rule (empty = nothing), recaptured after every
successful command (the Used reset stays at `/new` and `sessions
resume`) and, with `tui.WithStatusTick(d)`, on the Input loop every d
while the TUI waits for input — the region redraws only when the rows
changed, zero is off, and the tick never fires mid-turn (a buffered
tick lands immediately when the next Input starts). Without the tick,
rows change at command time only: a mid-turn tool effect shows stale
until the next command, unlike the swarm band, which rides a notify
event; a turn-end hook is a later version.
The swarm band is the one live surface the root does not compute: the
controller and the delegate tool emit `core.SwarmStatus` snapshots
(throttled, the exit always landing) and the TUI folds the latest into
the footer below the status rows, behind a short dim rule, while a
swarm runs (`workers <n> · +<pending> ✓<done> ✕<failed> · w<id> <task>
<age>`, `reviewer <n> · ⧗<review> ✓<done> ✕<failed> · w<id> <task>
<age>`), zero rows and no rule when nothing runs. A delegated batch —
the publisher whose role string is `delegate` — takes two rows:
`delegating · 3 workers · 1m12s` over the most recent call of any of
them (`#2 edit tool/file/edit.go · 12s`, `—` until a worker calls), the
same two rows for ten workers as for one, kept breathing between turns
by the frame ticker the batch holds. No new colors: labels and markers dim, counts text, the
check the success slot, the cross the fault slot; the glyph switch
carries the ascii fallback (`....`, `~` for the review clock).
The notices (`core.Notice`, source `swarm`) breathe once in the action indicator's row between turns, in their level's color, and commit nothing (2.11.7); before that they committed one dim line per decision in the
transcript (SPEC_SWARM 7). Both are status-string extensions: the
region's height-changing machinery covers them with no `live.go` line.
There is no session-start news line: the frontend reads no store
(SPEC_TUI), and a phase's own thinking is what fills the row now.

## theme

Two files, one resolver (`tui.ResolveTheme`): `theme.json` in the rig
home, and the `theme` string key in settings.json. The config package
carries both raw (`Config.Theme` is the theme.json document;
`Config.Settings.Theme` is the string) and the TUI owns the schemas:
`theme.json` is `base` (required, one of the shipped names), `slots`
(any of the slot names to a `#rrggbb`), and `glyphs` (`unicode` or
`ascii`), unknown keys refused in the TUI's voice; the settings key
must name a shipped palette or `custom` (config's voice for a wrong
type, the TUI's for an unknown name). Resolution is one dial: a set
key names the theme alone — `custom` names the file, which must
exist; with no key the file is the theme when present, else the
shipped default (`warm`). `/theme` (2.3.2) is the operator's hand on
the dial: it validates the preset, reads theme.json fresh when the
preset is `custom` (a missing file refuses by name), writes the key
with `config.SetTheme` (atomic, the other keys preserved), and pushes
the resolved theme at the TUI over an optional `RepaintTheme`
assertion — new output paints in the new theme, committed scrollback
keeps its bytes. A malformed theme.json refuses at start, naming the
file and the key.

## the key table

Enter submits (steers when a turn is live: the slot plus the interrupt;
with the completion menu open it accepts the selection into the input —
never dispatching — but only after a navigation keystroke, so an Enter
on a menu nobody moved still runs what was typed), Ctrl-C ends the session (interrupting a live turn first),
Ctrl-D is delete with text and session end at an empty prompt, Ctrl-T
toggles subsequent reasoning, Tab cycles the completion menu's selection
down (and completes a single candidate plus its trailing space), Shift-Tab
(CSI Z) steps it up, arrows and home/end move the cursor, backspace and
CSI-3~ delete, up/down walk the in-memory history, bracketed
paste mode is on at start (`?2004h`) so a paste arrives as one held
line: its newlines and tabs are literal text (a pasted Enter is `⏎` on
the row, not a submit, and one paste never dispatches a queue of
prompts), and every unrecognized control or CSI sequence is consumed and
ignored. Esc, outermost first: a pager open closes the pager;
else a menu open closes the menu (the input keeps its text); else, on an
empty prompt with a turn live, Esc interrupts the turn; else, the empty
prompt with no turn live being the gesture with nothing left to clear,
it stops every running delegated worker — the dashboard's stop button
is the same gesture — and a session with no delegate wired keeps the
prompt clear it always had. It is not a quit: the workers' own returns
still arrive, naming the interrupt as their exit (the reader names a
lone Esc by the grace window, a sequence's bytes arriving in one burst).

## the block formats

Tool block: the accent-glyph start line with the detail, the body at head
six and tail two with the dim hidden marker between, and the close line
with name, outcome glyph, and duration. Todo and scheduler replies are
parsed out of the tools' own reply text and re-rendered pane's way (the
progress bar fills done plus in-progress over the capped segments); a
reply that fails to parse commits raw, the degrade-to-CLI rule. The
command path prints the dim echo, then the reply in one of
two shapes (2.11.7): the list shape, repainted by `list_render.go` with
the ids in the dim slot and the state glyphs from the theme, or a
one-line ack. `todo` and `scheduler` keep their own blocks, and they are
the same functions the tool door uses, differing only in the opening
line. The list's row budget is the terminal's: `Env.Lines` carries the
live height into the store's `listLimit`, so a listing shows what fits
here.

## the theme tables

Sixteen named slots, five shipped palettes and the `oled` alias of
warm, two glyph sets. Warm and cool are the dashboard's two looks on
the same slots (warm is the default). The phosphor
ramps (p1 green, p3 amber) are four brightnesses of one hue: text on the
brightest, accent and success on the next, error, warn, and reasoning on
the middle, dim and rule on the deepest, so the state hierarchy survives
without hue and every slot maps to a pinned value. Colors are truecolor
hex; when the terminal reports no truecolor the nearest 256 index wins
(exact cube and grayscale matches included), named in the tests.
