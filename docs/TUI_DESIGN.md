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
at-most-three). Single-line edits (typing, the spinner tick) clear and
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

## the event map

The commit points are the events, exactly (SPEC_TUI decision 2):
ReasoningDelta and TextDelta stream as they arrive (reasoning dim and
only while the toggle is on), ToolStart switches the activity line to the
tool name, ToolResult commits the whole tool block, Done guarantees a
trailing newline and the status line's used takes its Usage, TurnEnd
commits the usage line and resets the live region to the input line,
Compacted commits the compact line and the status line's used takes the
compact's Kept (no block reprint), Fault commits the fault line, unknown
events are ignored. The activity label is the phase: thinking before and
between tools, the tool name while one runs. The spinner is state (a frame
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
`tui.WithNews(fn)` supplies the session-start news line (empty = nothing).
News is the latest run since the previous session in this cwd that failed
or is the job's first successful completion, one dim line, read-only.

## settings.theme

The `theme` key in settings.json is the TUI's (SPEC_TUI decision 7 calls
it the config's key; the frozen config loader still refuses it as
unknown). The root extracts it before the config load: if the user file
carries the key, the root rewrites the file without it into a shadow dir
alongside copies of the other config files and loads from there, so the
config package sees exactly the keys it owns. The value must be a string
(config's voice); the TUI's resolution validates it against the shipped
names and names the known.

## the key table

Enter submits (steers when a turn is live: the slot plus the interrupt;
accepts the menu's selection into the input when the menu is open, never
dispatching), Ctrl-C ends the session (interrupting a live turn first),
Ctrl-D is delete with text and session end at an empty prompt, Ctrl-T
toggles subsequent reasoning, Tab cycles the completion menu's selection
down (and completes a single candidate plus its trailing space), Shift-Tab
(CSI Z) steps it up, arrows and home/end move the cursor, backspace and
CSI-3~ delete, up/down walk the in-memory history, bracketed paste is
stripped to a plain byte stream so pasted newlines become ordered prompts
(the burst rule), and every unrecognized control or CSI sequence is
consumed and ignored. Esc, outermost first: a pager open closes the pager;
else a menu open closes the menu (the input keeps its text); else Esc
cancels the prompt whole (the reader names a lone Esc by the grace
window, a sequence's bytes arriving in one burst).

## the block formats

Tool block: the accent-glyph start line with the detail, the body at head
six and tail two with the dim elided marker between, and the close line
with name, outcome glyph, and duration. Todo and scheduler replies are
parsed out of the tools' own reply text and re-rendered pane's way (the
progress bar fills done plus in-progress over the capped segments); a
reply that fails to parse commits raw, the degrade-to-CLI rule. The
command path prints the dim echo, then the reply bytes restyled by the
theme, and the todo and scheduler renderers are the same function on both
doors, differing only in the opening line.

## the theme tables

Eight named slots, four shipped palettes, two glyph sets. The phosphor
ramps (p1 green, p3 amber) are four brightnesses of one hue: text on the
brightest, accent and success on the next, error, warn, and reasoning on
the middle, dim and rule on the deepest, so the state hierarchy survives
without hue and every slot maps to a pinned value. Colors are truecolor
hex; when the terminal reports no truecolor the nearest 256 index wins
(exact cube and grayscale matches included), named in the tests.
