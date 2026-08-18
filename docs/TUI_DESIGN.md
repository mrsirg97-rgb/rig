# TUI implementation notes

`specs/SPEC_TUI.md` is the design; the decisions below are the parts the
spec leaves to implementation. Each is pinned here because the golden
tests assert on the exact bytes.

## the pure core / shell split

Every renderer is a pure function: state in, bytes out, no I/O. `theme.go`
(palette and glyph tables, theme.json decode, the 256 downconvert),
`banner.go`, `commit.go`, and `tools_render.go` hold the renderers.
`live.go` turns (old live lines, committed chunk, new live lines) into the
escape stream. `input.go` is the key parser and line editor over a byte
stream. `tui.go` is the shell: the Frontend, the reader goroutine, the
command dispatch, and the reprint triggers. Tests drive the pure core
directly for the goldens and the TUI for the protocol.

## the live region protocol

Invariant: the live region is the last written lines (at most two: the
activity line and the input line) and the cursor is on the last live line
after its content. To commit a chunk and redraw: cursor up to the first
live line, to column 1, write the committed chunk (it overwrites the old
live lines as it flows), then write the new live lines. The old live
content is exactly the overwrite buffer, so a committed byte is never
rewritten and the only cursor arithmetic is up, column, and clear-line,
over at most the two live lines. Single-line edits (typing, the spinner
tick) are a clear-line and rewrite of the input or activity line in place.
The input line is truncated with runewidth to the terminal width so it
never wraps and the cursor invariant holds.

## the event map

The commit points are the events, exactly (SPEC_TUI decision 2):
ReasoningDelta and TextDelta stream as they arrive (reasoning dim and
only while the toggle is on), ToolStart switches the activity line to the
tool name, ToolResult commits the whole tool block, Done guarantees a
trailing newline, TurnEnd commits the usage line and resets the live
region to the input line, Compacted commits the compact line and reprints
the banner, Fault commits the fault line, unknown events are ignored.
The activity label is the phase: thinking before and between tools, the
tool name while one runs. The spinner is state (a frame index), not
time; a ticker goroutine advances it, and tests pin the frame.

## the banner and news seams

The TUI renders; the root computes. `tui.WithBanner(fn)` supplies the
banner numbers (model, effort, used over window, session up/down/cache);
`tui.WithNews(fn)` supplies the session-start news line (empty = nothing).
The root computes used as the last ContextTokens anchor plus
`compact.Estimate` of the messages after it, the compaction trigger's own
math over the assembled session. Session totals are one narrow join over
the state store's usage and messages rows. News is the latest run since
the previous session in this cwd that failed or is the job's first
successful completion, one dim line, read-only.

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

Enter submits (steers when a turn is live: the slot plus the interrupt),
Ctrl-C ends the session (interrupting a live turn first), Ctrl-D is delete
with text and session end at an empty prompt, Ctrl-T toggles subsequent
reasoning, arrows and home/end move the cursor, backspace and CSI-3~
delete, up/down walk the in-memory history, bracketed paste is stripped to
a plain byte stream so pasted newlines become ordered prompts (the burst
rule), and every unrecognized control or CSI sequence is consumed and
ignored.

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
