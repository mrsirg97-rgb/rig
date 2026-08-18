# rig: the TUI (roadmap deliverable 10)

The glass. A terminal frontend over the frozen runtime: the same events,
the same commands, the same tools, rendered in pane's design language
with fewer parts. The runtime is 0.3.0 and closed; this deliverable is
its first consumer and the freeze's first test: if the TUI needs a
`loop/` or `core/` line, the freeze was premature and 7 or 9 is
reopened first, per the roadmap. The done-when is structural: `frontend/
tui` implements `core.Frontend`, dispatches `core.Command`, and the
diff outside it is a registration path and two justified dependencies.

The design was settled by brainstorm before this spec; the decisions
below record it. The reference for taste is pane (`~/Projects/pane`:
builtin-restyle, footer, input, themes); the reference for what exists
to render is the event vocabulary (SPEC_HARDENING, SPEC_COMPACT) and
the command surface (SPEC_COMMANDS). Nothing here invents information;
every number and glyph is a reading of what the runtime already emits.

## goals

- Scrollback-native: the terminal owns history; rig decorates a small
  live tail. No alternate screen, ever (decision 1).
- Committed blocks on event boundaries: tool rows with glyphs and
  durations, dim reasoning, command output, the startup block
  (decisions 2-5).
- The status line: one dim live row under the input, the model and
  the context used over the window, updated from the usage events;
  the banner's other content commits once, at session start
  (decision 3, amended).
- One renderer per tool for todo and scheduler, used byte-identically
  by the tool-result path and the command path (decision 6).
- Four shipped themes (`oled`, `paper`, `p1`, `p3`), a glyph table with
  an `ascii` set, and `theme.json` as a user palette: this spec owns
  the schema SPEC_CONFIG reserved (decision 7).
- Retro as texture, never information (decision 8).
- Input: single-line editing with history, a completion menu over the
  command names and their argument hints, the existing steering and
  Ctrl-C semantics unchanged (decision 9, amended).
- Two dependencies, named and capped: `golang.org/x/term` and
  `github.com/mattn/go-runewidth` (decision 10).

## non-goals

- No alternate screen, no cell grid, no owned scrollback, no scroll
  physics, no copy mode, no mouse: the terminal and tmux already do
  these (decision 1's rejection).
- No fonts and no painted backgrounds: the terminal owns both; the
  docs recommend a setup (decision 7).
- No pinned header: the status line is live, not pinned, and the
  startup block is committed, not pinned. A pinned variant is named
  future work, not built (decision 3).
- No multiline editor: pasted lines are separate prompts (the 0.2.0
  burst rule); a heredoc-style composer is a later extension on the
  same seam.
- No scanlines, flicker, ANSI-art banners, or any retro effect that
  costs legibility (decision 8's rule).
- No scrollback search, no tabs or windows, no images, no progress
  animations beyond the spinner.
- No new events, no new commands, no loop line, no core line: the
  vocabulary is closed; the TUI reads it.
- No config beyond `settings.theme` and `theme.json` (SPEC_CONFIG's
  seam): no TUI-specific settings file.

## layout

```
frontend/tui/         NEW package, main module (decision 10 names the
                      module question and the rejection)
  tui.go              the Frontend: Input, Notify, the dispatcher
  live.go             the live region: the menu, activity, pending,
                      input, and status rows; cursor-up redraw, width
                      handling, the one-op-one-write frame
  commit.go           committed blocks: turn text, reasoning, tool
                      rows, command output, the usage line
  status.go           the status line, the startup block, and the
                      snapshot's refresh points
  tools_render.go     the todo and scheduler block renderers (one
                      renderer, both doors)
  input.go            raw mode, the key parser (Tab and Shift-Tab),
                      single-line editing, history, the completion
                      menu's state
  theme.go            the palette table (oled, paper, p1, p3), the
                      glyph table (unicode, ascii), theme.json schema
                      and merge
  ansi.go             escape helpers: color, cursor, clear-line
  *_test.go           golden blocks per theme at 50 and 100 columns,
                      escape-capture live-region cases, both-doors
                      byte-equality, theme.json schema cases
cmd/rig/main.go       the -tui flag (default: auto: TUI when stdout is
                      a terminal, plain CLI when piped); the theme
                      resolution from settings + theme.json
docs/SETUP.md         the recommended terminal setup (fonts, an
                      OLED-black profile) and the theme keys
```

## decisions

### 1. Scrollback-native; the alternate screen rejected

The terminal owns history. rig prints committed blocks into normal
scrollback and redraws only a live region at the bottom (the cap,
decision 2: the menu's rows, the input's up to five rows, and the
status row). Scrolling is the terminal's and tmux's;
resize costs nothing for history; phone-over-ssh behaves; `tmux
capture-pane` and copy-mode keep working; a crashed TUI leaves a
readable transcript in the terminal.

Rejected, named: the alternate screen (bubbletea/tcell family). It
buys in-place editing of old output and pays for it with owned
scrollback, scroll physics, copy mode, resize repaints, and a
render-loop framework that would fight the runtime's own event loop
(`Notify` already is the loop). The one cost of scrollback-native is
accepted and named: committed output is immutable; an expanded tool
preview is reprinted below, never edited in place.

The one modal exception, named: the pager (copy-mode). Some terminals
give the operator no way up the scrollback (a web tty, a phone), so
PgUp opens a bottom-anchored view of the committed history on the
alternate screen, the way less borrows it: PgUp/PgDn page, the arrows
step a line (an emulator's wheel arrives as arrows on the alt screen),
Home/End jump, and q, Esc, or Enter restore the main screen exactly.
The document is the live region's own record of committed lines
(painted, ring-capped at 5000); while the pager is up the live region
suspends — its bookkeeping runs, its writes stop, and commits queue —
and the return replays the queue. The main UI itself never enters the
alternate screen.

### 2. The live region and the commit points

Everything above the live region is immutable printed history.
Everything dynamic lives in the last lines, top to bottom: the
pending prose line while one is open, the activity line during a turn
(the spinner, then the current phase: `- thinking`, `| bash`), the
completion menu when one is open (decision 9: its candidate rows,
then its tail row), the input line (typing steers, as today), and the
status line (decision 3) — always the region's last row. The activity
line locks directly above the menu and input: streamed text flows
into scrollback above the loader, never under it (the amended order;
the first order put the loader above the pending line, and a wrapping
stream grew under it).

- during a turn: the pending line while it is open, the activity line
  under it, the input line under that;
- between turns: the input line, the status line under it.

The cap, amended over decision 1's at-most-three: up to six menu
rows, the menu's tail row when the candidates run past six, the input
(itself up to five rows), and the status row. The menu rows are live
rows: repainted in place, never committed — the wf suspension gate
keeps them off the pager's screen, like every other live row.

The commit points are the events, exactly:

- `ReasoningDelta` / `TextDelta`: streamed as they arrive (reasoning
  dim, text normal), committed as flowed: the terminal wraps, rig
  never hand-wraps committed prose (decision 8's sinkhole rule);
- `ToolStart`: the activity line switches to the tool; `ToolResult`:
  the tool row commits with glyph, detail, outcome, duration
  (decision 4);
- `Done`: the turn's text is complete, and the status line's used
  takes its `Usage` (decision 3);
- `TurnEnd`: the usage line commits (`up 3.2k down 136 · cache r 918
  92%`, pane's shaping) and the live region resets;
- `Compacted`: the compact line commits, and the status line's used
  takes the compact's `Kept` (decision 3);
- unknown events: ignored (the compat rule; the CLI's discipline).

The live region redraw is cursor-up, clear-line, reprint: the only
cursor arithmetic in the program, over at most the cap. Committed
lines are `fmt.Fprint` and the terminal's own wrapping. On resize only
the live region repaints (SIGWINCH; history is the terminal's
problem, already solved).

One op, one write (the write gate, `live.go`): an op's escapes and
rows are buffered and flushed to the terminal as a single write, so
the terminal sees every repaint whole — no partial frame between the
clear and the reprint, and no row left ending exactly at the last
column across a write boundary. The tear this names: a repaint was
many small writes, and a row written to exactly the width leaves the
terminal's deferred wrap pending at the write boundary; a terminal
that resolves the wrap at the flush shifts the cursor a row, and the
next op's cursor-up tally is off by a row — the clear misses the top
row (the indicator) and it lands between committed text.

### 3. The status line: one live row, a one-shot startup block

The two-row banner is deleted; its content gets the two homes
named here.

The live half is the status line: the region's last row (decision 2),
dim, `model · used/window` —

```
huihui3.8 · 41.2k/262k
```

— before the first usage, the model alone. The context part keeps
the banner's 70/90 coloring (dim under 70, warn at 70, error at 90;
`formatTokens` shaping). This is the always-visible info region the
banner gave up on, and the named cost inverts: the mid-turn context
number is now live, and the identity row is read once, at start.

`used` is the frontend's own arithmetic over the usage events it
already receives (the seam does not move): the last `Done`'s
`Usage.Prompt + Usage.Completion` — exactly the `ContextTokens` the
loop stamps on the last assistant message, the compaction trigger's
anchor — and, after a `Compacted`, the compact's `Kept` (the
trigger's own estimate of the kept tail). The model identity and the
window come from the root's closure (the banner's old door),
refreshed at the call points the banner reprint used to fire —
session start, `/new`, `sessions resume`, and a `models` switch —
and never per repaint: the closure is a store read, and a live row
repaints on every keystroke.

The committed half is the startup block (the choice, named: one
committed block at session start, not deleted): the greeting in the
accent, the session id under it (its first twelve characters, git's
short-hash habit; `/sessions` lists the full ids), then the banner's
identity row
without its context part, its usage row, and the scheduler news line
(decision 6) when there is one — dim, no dotted rules (they enclosed
the banner, and the banner is gone):

```
welcome to rig
session 2f9a1c0e77b3
huihui3.8 · xhigh
up 214k down 18.2k · cache r 187k 92%
· j5 failed 14:30 · scheduler runs j5
```

The reprint triggers are amended to match: `/new`, `sessions resume`,
and a `models` switch refresh the status line's snapshot (and reset
its used number where the session did: `new`, `resume`); a
`Compacted` no longer reprints a block at all. Between refreshes the
per-turn usage line keeps the running numbers in the scrollback where
they happened (decision 2, unchanged).

A pinned one-line header via scroll region is named future work
(`-pin-header`), not built: inside a margin region most terminals
drop scrolled lines from scrollback, which trades away decision 1.

### 4. Tool rows

One committed block per execution, pane's vocabulary:

```
● bash · $ go test ./middleware/
  ok  	rig/middleware/guard	0.4s
bash ✓ 0.4s
```

- `ToolStart` opens the row: accent glyph, tool name, the detail;
- the result body renders head/tail: first N and last M lines with a
  dim `· k lines elided ·` between (N=6, M=2 at v1; the caps are the
  TUI's, the runtime's own output caps still apply first);
- `ToolResult` closes it: name, `✓`/`✕`, duration; a fed-back failure
  (`Err` non-nil) renders `✕` and the content stays visible: the
  refusal is the interesting part.

The detail line per tool is a table in `tools_render.go`, one line
each: bash the command, read/write/edit the path, ls/find/grep the
pattern or path, python the first line of code, web_search the query,
web_fetch the url, todo/scheduler the action (their blocks are
decision 6's). Unknown tools (a future registration) render name-only:
the table is a nicety, not a contract.

### 5. Reasoning and command output

Reasoning streams dim and commits dim, visible by default: at this
fleet's decode speeds the thinking is the show, and the operator reads
it (the reading-band fact behind the no-MTP brain). A keystroke
(Ctrl-T, pane's binding) toggles rendering of subsequent reasoning;
committed history is immutable (decision 1). The transcript and the
wire are untouched either way: this is display only.

The committed prompt line is a command's only echo (a separate dim
copy was built and removed: it doubled the line); the output commits
as a plain block, exactly the CLI's bytes: SPEC_COMMANDS' output contracts are
the render, restyled only by theme color. The scheduler session-start
line (decision 6) and the startup block (decision 3) are the only
unprompted blocks.

### 6. todo and scheduler: one renderer, both doors

The differentiating rule, testable: tool render blocks for todo and
scheduler are owned by `tools_render.go` and used by both the
tool-result path (the model called the tool) and the command path (the
operator typed `/todo ...`). A rendering that differs between the two
is a bug; the golden tests assert byte equality minus the opening line
(`● todo · start t3` vs `/todo · start t3`).

The blocks are pane's:

```
● todo · start t3
  ▰▰▰▱▱ 2/5 · next t4
  ● t1 wire the models table
  ◐ t3 the switch seam
  ○ t4 steer verb · waits on t3
```

- the progress head, then one row per task: status glyph, id, text,
  `· waits on tN` dim when blocked, `· claimed by <sid8>` dim only
  when the claim is foreign (another session);
- scheduler `list`: `●`/`○`/`✕` per job state with cron, last, next,
  and drift named; `runs`: the run lines with tail previews.

The renderers parse the tools' own reply text (the queue the reply
already carries): no new tool surface, no reaching into stores from
the render path. If parsing fails (a future voice change), the raw
reply commits as-is: degrade to the CLI, never hide.

One ambient line, only one: at session start, in the startup block
(decision 3), if the workspace scheduler store has news since the last
session in this cwd (a failed or first-completed run), one dim line:
`· j5 failed 14:30 · scheduler runs j5`. Absent news, nothing. This
is a read of the store the root already opens, the exact analog of
todo's stale footer; it is the only place the TUI reads a store, and
it is read-only.

### 7. Themes: the palette table, the glyph table, theme.json

No fonts, no painted backgrounds: the terminal owns both, and painting
backgrounds in scrollback-native produces seams. Deep black is the
operator's terminal profile; `docs/SETUP.md` gains a recommended
setup: an OLED-black profile and a font shortlist (Berkeley Mono,
Departure Mono, Terminus, IBM 3270). rig's palettes are tuned to pop
on true black.

The palette is a table of named slots, four shipped:

- `oled` (default dark): pane's hues re-tuned for `#000`: dims lifted,
  accents brightened;
- `paper` (light): ink on near-white, each hue picked for paper, not
  mathematically inverted;
- `p1` (green phosphor) and `p3` (amber): monochrome ramps, four
  brightnesses of one hue; the deepest retro and a standing test that
  the glyph hierarchy survives without hue.

Slots (the schema's vocabulary, fixed here): `text`, `dim`, `accent`,
`success`, `error`, `warn`, `rule`, `reasoning`. Colors are truecolor
hex; when the terminal reports no truecolor (`COLORTERM` absent), rig
downconverts to the nearest 256-color index: named, automatic, not
configurable.

The glyph table has two sets: `unicode` (`○ ◐ ● ✕ ⧉ ❯ ▰ ▱ ·`) and
`ascii` (`[ ] [~] [*] [x] = > # - .`): the deepest-retro look and the
compatibility fallback, one mechanism.

Selection and override:

- `settings.theme`: one of the shipped names (SPEC_CONFIG's key;
  unknown refuses loud at start naming the known);
- `theme.json` (the file SPEC_CONFIG reads raw and this spec owns):

```json
{
  "base": "oled",
  "slots": { "accent": "#ff9e64", "reasoning": "#5a5a5a" },
  "glyphs": "ascii"
}
```

  `base` names a shipped theme (required; unknown refuses); `slots`
  overrides named slots (unknown slot names refuse, naming the
  vocabulary; values must parse as `#rrggbb`); `glyphs` selects the
  set. `theme.json` present with `settings.theme` also set: the file
  wins (it is the more specific intent); named. Malformed refuses at
  start in SPEC_CONFIG's voice.

### 8. Retro is texture, never information

The governing sentence: color and glyphs carry state exactly as the
design language defines; the retro layer touches case, rules, the
spinner, and palette only. The rules:

- all lowercase, everywhere: headers, refusals, the status line and
  the startup block (the house voice, committed to);
- rules are dotted (`·`) lines — they enclosed the deleted banner
  (decision 3), and no block draws one now; no box drawing at all;
- the spinner is `|/-\`, four frames, on the activity line only;
- durations and counts stay plain (`0.4s`, `12k`): nothing humanized,
  nothing animated;
- rejected by the rule, named: scanline effects, deliberate flicker,
  ANSI-art banners, gradient text. Each would cost legibility to
  decorate it.

Wrapping, the second sinkhole rule: committed lines are wrapped by the
terminal (print and flow); rig truncates only tool previews and does
so loudly (`· k lines elided ·`). The TUI never measures committed
prose; it measures only the live region (menu rows included, decision
9) and the startup block (runewidth, decision 10), both built to fit
50 columns.

### 9. Input

v1 is deliberately small: one logical line, edited with cursor
left/right, home/end, backspace/delete across the line. The line wraps
across up to five terminal rows as it grows (the terminal wraps, the
live region's row math places the cursor); a longer text scrolls a
five-row window that follows the cursor, and the Enter always commits
the full text. History (up/down, in memory, session-scoped), and
exactly the existing semantics for
everything else: typing during a turn steers (the slot, the interrupt);
`/` is the command prefix with `//` the escape; Ctrl-C ends the
session; Ctrl-D at an empty prompt exits; Ctrl-T toggles reasoning
(decision 5).

Bracketed paste: the TUI turns the mode on with raw mode (and off at
Close), and a paste is ONE input — its newlines are text, shown as the
return mark on the row and committed as real newlines; its control
bytes are inert; a CRLF folds to one newline. Only the typed Enter
submits. A terminal without the mode keeps the burst rule (pasted
lines as separate ordered prompts — the CLI's, and the CLI keeps it
everywhere).

The kill keybinds, readline's: Esc cancels the prompt whole (the
history draft with it; the reader names a lone Esc by the grace
window, since a sequence's bytes arrive in one burst); Ctrl-U kills to
the start, Ctrl-K to the end, Ctrl-W the word before the cursor.
Esc precedence, outermost first, amended: a pager up closes the
pager; else a menu open closes the menu (the input keeps its text);
else Esc cancels the prompt whole, as before.

Command lines get completion while being typed, one machinery for the
name and its arguments. The candidates are the known command names
with the typed prefix while the name is being typed, and, after a
complete name and a space, the command's argument hints — an
additive, optional interface in `command` (`Sub() []Sub`,
`Sub{Name, Desc string}`): the verb commands implement it (`/todo`
its actions, `/models` the table's known names from the Env, their
roles the descriptions). A command without hints keeps today's
behavior: its `Description()` as the ghost. Frontend-only: the seam
does not move, and the CLI never sees the menu.

- two or more candidates: the menu, above the input, inside the live
  region (decision 2's cap): one row per candidate, `name  desc`, the
  name in the accent, the description in text, the selected row
  inverted. A menu row is one terminal row (decision 10: the live
  region is measured): the description takes what the width leaves
  after the name and is dotted when it overflows, never wrapped. At
  most six rows show; the window follows the selection like the
  input's five-row window does, and a dim `… N more` tail counts the
  candidates past the window. Tab cycles the selection down, Shift-Tab
  up (CSI Z, added to the parser), Enter accepts the selection into
  the input — the typed prefix replaced by the candidate, a trailing
  space, never dispatching — and Esc closes the menu until the input
  changes.
- exactly one candidate: the ghost, today's rule — the remainder, dim,
  display only, drawn only when the cursor is at the end and the line
  fits — and Tab completes it, plus the trailing space. Enter over a
  showing ghost completes first and then submits: the row promised the
  completion, and the typed prefix alone would dispatch as an unknown
  command (`/m⏎` runs `models`).
- the phases never mix: name candidates while the name is typed,
  argument candidates after the name and a space, nothing for plain
  prompts and the `//` escape.

Raw mode via `x/term`; the key parser covers the named keys (Tab and
Shift-Tab included) and ignores unrecognized sequences (never crash on
an exotic terminal).
Named gaps, deliberate: no multiline composer, no history persistence.
Each is a later extension inside `input.go`; none touches a seam.

### 10. Placement and dependencies

`frontend/tui` lives in the main module, and the roadmap's "own
module, own version line" sentence is amended by this spec, named: a
separate module must either duplicate the root's wiring (a second
`main` that rebuilds stores, config, recorder, commands) or force the
root's wiring into an exported package, a refactor the freeze exists
to avoid. The TUI is a Frontend like `frontend/cli`; it belongs where
that one lives. The version line stays the runtime's; the TUI's
maturity is tracked by the roadmap, not a second module version.

The two dependencies, justified once: `golang.org/x/term` (raw mode
and size; the quasi-stdlib), `github.com/mattn/go-runewidth` (glyph
and CJK width for the live region and previews; hand-rolling width
tables is the known wrong move). Both are leaf deps of `frontend/tui`
only. Rejected, named: bubbletea, lipgloss, tcell (decision 1's
frameworks); a color-conversion dep (the 256 downconvert is thirty
lines).

Entry: `-tui` on the root, default `auto`: the TUI when stdout is a
terminal, the plain CLI when piped or redirected (`-tui=false` forces
plain; scripts and the e2e keep the CLI bytes). `-p` and `run-job`
never engage the TUI. The CLI frontend remains, untouched: it is the
piped mode and the reference rendering.

## testing

Named cases, failing first. The renderer is pure (events in, bytes
out) wherever possible; raw-mode and resize cases run behind a pty
where the CI box allows and skip cleanly where not.

- golden committed blocks: a scripted event stream (text, reasoning,
  a tool round trip, a compaction, a turn end) rendered at 50 and 100
  columns, in `oled` and in `ascii`+`p1`: the exact bytes. The same
  stream through the plain CLI still matches the CLI's goldens (the
  TUI adds, never changes, the CLI).
- the startup block and the status line: the block's exact bytes from
  a scripted session (the identity row, the session row, the news line
  when there is one), committed exactly once at start and by no other
  event or command; the status row is the model alone before the
  first usage, `model · used/window` after a `Done` (its
  Prompt + Completion), and the compact's `Kept` after a `Compacted`;
  the snapshot (model, window) refreshes on `new` / `sessions
  resume` / a `models` switch and the root's closure is read at those
  points only.
- both doors: the todo and scheduler blocks byte-equal between the
  tool-result path and the command path, minus the opening line.
- the live region: an escape-capture harness asserts the redraw is
  cursor-up/clear/reprint over at most the region's cap (decision 2),
  and that committed bytes are never rewritten (the immutability
  invariant, decision 1); one op is one write to the terminal (the
  write gate, decision 2).
- the tear: a stream of multi-line wrapped reasoning, replayed write
  by write through a flush-aware vt (a pending wrap resolves at a
  write boundary, the way a terminal's flush may), lands no indicator
  row between committed lines and the screen's rows exact; the same
  stream through the plain deferred-wrap vt stays exact (both models
  named, decision 2).
- input: the key parser table (arrows, home/end, backspace across a
  wide glyph, Shift-Tab as CSI Z, an unrecognized CSI ignored);
  history up/down; paste of three lines becomes three prompts in
  order (the burst rule, through the TUI's reader); Ctrl-T toggles
  subsequent reasoning only; the completion menu (the rows' exact
  bytes — the name in the accent, the selected row inverted — the
  six-row cap with the `… N more` tail, the window following the
  selection, Tab down and Shift-Tab up cycling, Enter accepting the
  selection without dispatching, Esc closing the menu and the
  precedence pager > menu > prompt-clear, the menu reopening when the
  input changes) — and live-row behavior: the menu repaints in place
  and never commits, and stays off the pager's screen while it is up;
  the argument hints (`/todo`'s actions, `/models`' known names and
  roles, the unique-candidate ghost and Tab completing it with the
  trailing space, a command without hints keeping its description
  ghost); a unique command name keeps today's completion (the full
  name, the trailing space).
- themes: theme.json schema cases (unknown base, unknown slot, bad
  hex, glyphs value), the file-wins-over-settings rule, the 256
  downconvert (a known hex to a known index); the p1 ramp renders
  every state distinctly (the monochrome legibility test, asserted on
  distinct SGR sequences, not by eye).
- the scheduler news line: news since last session renders one dim
  line; no news renders nothing; the read is read-only (no store
  mutation; asserted on the store file's bytes).
- the freeze gate: the PR's diff outside `frontend/tui`, `cmd/rig`,
  `docs/`, and `go.mod`/`go.sum` is empty; `core/` and `loop/` are
  byte-identical; the full suite including every CLI golden is green.

## scope

What 10 is not: a rewrite of the CLI (it stays, as the piped mode and
the reference bytes); a place for new verbs or events (the vocabulary
is closed; anything the glass wants and cannot render from existing
events is a finding to bring back to the roadmap, not to patch in); a
notification system, a dashboard, or a second store reader beyond the
one named line; a config surface beyond `settings.theme` and
`theme.json`.

After 10: the field test. The version stays 0.x until rig is the
daily driver and the TUI has survived real use on the phone; the 1.0
tag is earned by soak, not by shipping the glass (the roadmap's
criterion, unchanged).
