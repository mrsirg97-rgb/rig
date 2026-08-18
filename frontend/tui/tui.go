package tui

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
)

// tui is the terminal Frontend (SPEC_TUI): the reader goroutine, the
// command dispatch, and the banner reprint triggers, over the pure
// renderers (decisions 3, 5, 6) and the live-region protocol (decision
// 2). The runtime contract is the CLI's: one slot, latest wins; the
// steering and Ctrl-C semantics unchanged (decision 9); the loop's
// contract "Input returns one message" a prompt, never a command.
type tui struct {
	theme Theme
	in    io.Reader
	fdi   int // the input fd: raw mode and the size (0 = not a tty)

	mu            sync.Mutex
	width         int
	live          *live
	inputText     string // the line's text, rendered under mu
	editPos       int    // the edit position, mirrored under mu
	phase         string // the activity line's label: thinking, or a tool
	frame         int    // the spinner's frame (|/-\)
	showReasoning bool
	turnLive      bool
	// turnEstablished: the live turn has produced an event (the
	// burst/steer boundary, decision 9's "pasted lines are separate
	// ordered prompts" vs "typing during a turn steers"): a line that
	// lands before the turn's first event is a prompt in order; one
	// that lands after steers (the slot, the interrupt).
	turnEstablished bool
	reading         bool // Input is in flight
	turnCtx         context.Context
	cancel          context.CancelFunc
	slot            string
	hasSlot         bool
	steeredLive     bool
	bannered        bool
	quit            bool
	rawOld          *term.State
	// per-turn usage totals (Done accumulates, TurnEnd commits them):
	prompt, completion, cacheRead int
	// the turn's pending line: the streamed bytes that have not crossed
	// a newline yet, as painted segments (decision 2: committed as
	// flowed — the terminal wraps, rig never hand-wraps). It is the
	// live region's middle row (decision 1's third line); only closed
	// lines reach scrollback.
	pend []seg
	// the current ToolStart: the block's detail line (decision 4).
	toolName string
	toolArgs []byte

	bannerIn func(context.Context) BannerIn
	news     func(context.Context) string
	commands map[string]core.Command
	known    []string
	env      any

	// reader-owned: the line state and the byte-stream parser.
	ed editor
	kp keyParser

	pending    chan string
	wake       chan struct{}
	readerOnce sync.Once // the reader starts on the first Input, after the first draw
	closed     chan struct{}
	closeOnce  sync.Once
	ticker     *time.Ticker

	ticks <-chan time.Time
	winch <-chan struct{}
}

// Option configures the frontend.
type Option func(*tui)

// WithTheme is the resolved palette (decision 7's selection, the
// root's ResolveTheme over settings.theme and theme.json).
func WithTheme(t Theme) Option { return func(tu *tui) { tu.theme = t } }

// WithWidth is the terminal's width (the production default is the tty
// size at start; the tests name it).
func WithWidth(w int) Option { return func(t *tui) { t.width = w } }

// WithBanner is the banner's numbers (decision 3): the root computes
// them at call time (the session start, and every reprint trigger).
func WithBanner(f func(context.Context) BannerIn) Option {
	return func(t *tui) { t.bannerIn = f }
}

// WithNews is the scheduler's session-start line (decision 6): one dim
// line when the workspace store has news since the last session in this
// cwd, empty when it does not. The read is the root's (a store it
// already opens); the TUI renders it and nothing else.
func WithNews(f func(context.Context) string) Option {
	return func(t *tui) { t.news = f }
}

// WithCommands registers the user commands (SPEC_COMMANDS 1): the
// prefix dispatch inside Input, before a line becomes a prompt. The
// Steer seam is the frontend's, filled here (as the CLI's).
func WithCommands(cmds []core.Command, env any) Option {
	return func(t *tui) {
		t.commands = make(map[string]core.Command, len(cmds))
		t.known = make([]string, 0, len(cmds))
		for _, cmd := range cmds {
			t.commands[cmd.Name()] = cmd
			t.known = append(t.known, cmd.Name())
		}
		sort.Strings(t.known)
		t.env = env
		if e, ok := env.(*command.Env); ok {
			e.Steer = t
		}
	}
}

// WithTicks is the spinner's clock (a test seam: a manual channel; the
// production default is a 120ms ticker).
func WithTicks(ch <-chan time.Time) Option { return func(t *tui) { t.ticks = ch } }

// WithWinch is the resize seam (a test seam: a manual channel; the
// production default is SIGWINCH on the input tty).
func WithWinch(ch <-chan struct{}) Option { return func(t *tui) { t.winch = ch } }

// New wires the frontend over an input reader and an output writer.
// Raw mode engages when the input is a terminal (x/term, decision 10);
// the reader goroutine starts here, as the CLI's.
func New(in io.Reader, out io.Writer, opts ...Option) core.Frontend {
	t := &tui{
		theme:         defaultTheme(),
		in:            in,
		width:         80,
		showReasoning: true,
		ed:            newEditor(),
		pending:       make(chan string, 16),
		wake:          make(chan struct{}, 1),
		closed:        make(chan struct{}),
	}
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		t.fdi = int(f.Fd())
		if w, _, err := term.GetSize(t.fdi); err == nil && w > 0 {
			t.width = w
		}
		if old, err := term.MakeRaw(t.fdi); err == nil {
			t.rawOld = old
		}
	}
	for _, opt := range opts {
		opt(t)
	}
	t.live = newLive(out, t.width)
	if t.ticks == nil {
		t.ticker = time.NewTicker(120 * time.Millisecond)
		t.ticks = t.ticker.C
	}
	if t.winch == nil && t.fdi != 0 {
		t.winch = signalWinch()
	}
	if t.ticks != nil {
		go t.tickLoop()
	}
	if t.winch != nil {
		go t.winchLoop()
	}
	return t
}

// defaultTheme is the shipped default (decision 7: oled, truecolor
// when the terminal reports it — the root's ResolveTheme is the real
// door; this is the bare New's fallback).
func defaultTheme() Theme {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		panic("tui: " + err.Error())
	}
	return th
}

// signalWinch is the production resize seam: SIGWINCH on the input
// tty (decision 2: on resize only the live region repaints).
func signalWinch() <-chan struct{} {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	ch := make(chan struct{}, 1)
	go func() {
		for range sig {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}()
	return ch
}

// IsTerminal reports whether fd is a terminal (x/term, decision 10's
// raw-mode dependency). The root's auto mode (-tui's default) asks it
// of stdout.
func IsTerminal(fd uintptr) bool { return term.IsTerminal(int(fd)) }

// Close stops the goroutines and restores the terminal's mode. The
// Frontend's two methods need no close (the process owns the
// lifetime); the root's and the tests' do.
func (t *tui) Close() {
	t.closeOnce.Do(func() { close(t.closed) })
	if t.ticker != nil {
		t.ticker.Stop()
	}
	if t.rawOld != nil {
		term.Restore(t.fdi, t.rawOld)
	}
}

// --- the reader goroutine -------------------------------------------

// readLoop owns the input bytes (decision 9): the parser and the
// editor are its state, and the routing is the CLI's (decision 9's
// "exactly the existing semantics"): a line that lands while a turn is
// live steers (the slot, the interrupt); a line that lands between
// turns delivers in order (the burst rule, the slot's latest-wins
// would silently drop all but the last line of a paste).
func (t *tui) readLoop() {
	br := bufio.NewReader(t.in)
	for {
		b, err := br.ReadByte()
		if err != nil {
			t.onInputEOF()
			return
		}
		if k, r := t.kp.next(b); k != keyNone {
			t.onKey(k, r)
		}
	}
}

// onKey applies one parsed key to the line (the editor) and repaints;
// the named control keys do their thing (decision 9).
func (t *tui) onKey(k key, r rune) {
	switch k {
	case keyEnter:
		t.onEnter()
	case keyCtrlC:
		// ends the session (9): the live turn is interrupted, the
		// next Input ends the loop.
		t.quitSession()
	case keyCtrlD:
		// at an empty prompt, exits (9); a non-blank line is kept.
		if strings.TrimSpace(t.ed.text()) == "" {
			t.quitSession()
		}
	case keyCtrlT:
		// toggles the rendering of subsequent reasoning (decision 5):
		// committed history is immutable, the transcript untouched.
		t.mu.Lock()
		t.showReasoning = !t.showReasoning
		t.mu.Unlock()
	case keyTab:
		// tab completes a command name against the known set (decision
		// 9's hint): to the longest common prefix, plus the trailing
		// space when the match is unique. Anywhere else, ignored.
		t.completeCommand()
		t.paintInput()
	default:
		t.ed.apply(k, r)
		t.paintInput()
	}
}

// paintInput rewrites the input row in place and parks the terminal
// cursor at the edit column (the edit op, live.go).
func (t *tui) paintInput() {
	t.mu.Lock()
	t.inputText = t.ed.text()
	t.editPos = t.ed.pos
	line, col := t.inputLineAndColLocked()
	t.live.edit(line, col)
	t.mu.Unlock()
}

// onEnter submits the line (the editor consumes it): the Enter path
// (live.go) freezes it, and the routing is the CLI's — a live turn
// takes the slot and the interrupt; a quiet prompt delivers in order.
func (t *tui) onEnter() {
	line, submitted := t.ed.apply(keyEnter, 0)
	if !submitted || strings.TrimSpace(line) == "" {
		return // a blank line is a no-op, consumed (the CLI's rule)
	}
	t.mu.Lock()
	full := t.theme.Paint(SlotAccent, t.theme.Glyph(GlyphPrompt)) +
		t.theme.Paint(SlotText, " "+line)
	t.inputText = ""
	t.editPos = 0
	wasLive := t.turnLive
	established := t.turnEstablished
	isCmd := t.commands != nil && command.IsCommandLine(line)
	if !wasLive && !isCmd {
		// this line will start a turn: the live flag goes now (the
		// delivery's insertActivity places the activity row, exactly
		// once — the reader's and the delivery's rows would be two).
		// a command line never starts a turn — it dispatches, and the
		// turn's state must not move for it.
		t.turnLive = true
		t.phase = "thinking"
		t.frame = 0
	}
	// a steering line: the turn stays live (unwinding), the activity
	// row above is left standing.
	t.live.enter(full, "", t.inputLineLocked())
	t.mu.Unlock()
	if isCmd {
		t.pending <- line
		return
	}
	if wasLive && established {
		// the turn is established (an event has crossed): this is
		// steering — the slot, latest wins, and the interrupt.
		// the pending line stays on the screen as scrollback (the
		// enter above froze the input row beneath it), and its state
		// goes with it: the next turn starts a fresh line.
		t.mu.Lock()
		t.pend = nil
		t.mu.Unlock()
		t.steer(line)
		return
	}
	// a quiet prompt, or a paste before the turn's first event: the
	// lines deliver in order (the burst rule, decision 9) — the slot's
	// latest-wins would silently drop all but the last of a paste.
	t.pending <- line
}

// steer queues the line (one slot, latest wins) and interrupts the
// live turn (the loop breaks at its next boundary). A dead handle is a
// no-op: the queued line simply waits for the next Input.
func (t *tui) steer(line string) {
	t.mu.Lock()
	t.slot = line
	t.hasSlot = true
	t.steeredLive = true
	cancel := t.cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// quitSession is Ctrl-C/Ctrl-D/EOF: the flag is sticky (the next Input
// ends the loop with io.EOF, as the CLI's), and a live turn is
// interrupted so the loop reaches that Input promptly.
func (t *tui) quitSession() {
	t.mu.Lock()
	t.quit = true
	if t.turnLive && t.cancel != nil {
		t.cancel()
	}
	t.mu.Unlock()
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// onInputEOF is the reader's end: the CLI's final-line rule (a line
// without a trailing newline is delivered), then the quit and the
// channel's close (the blocked Input's io.EOF).
func (t *tui) onInputEOF() {
	if line, submitted := t.ed.apply(keyEnter, 0); submitted && strings.TrimSpace(line) != "" {
		t.pending <- line
	}
	t.quitSession()
	close(t.pending)
}

// --- the Frontend ----------------------------------------------------

// Input blocks for one user message (the loop's contract). The
// steering slot is delivered before blocking (the contract, as the
// CLI's); a command line is dispatched and consumed there, before any
// line becomes a prompt (SPEC_COMMANDS 1); blank lines are no-ops; EOF
// ends the REPL; a cancelled context surfaces its error.
func (t *tui) Input(ctx context.Context) (string, error) {
	t.mu.Lock()
	if cancel, ok := core.InterruptFrom(ctx); ok {
		t.cancel = cancel
	}
	t.reading = true
	// the quit is the channel's, not a flag checked here: a line the
	// reader pushed before the quit (the CLI's final-line rule) must
	// deliver before the EOF, whatever the race's order (the CLI
	// reference: the line is sent before the close, always first).
	if !t.bannered {
		// the session start: the banner and the news line, committed
		// exactly once (decision 3's other triggers reprint from
		// dispatch and the Compacted event).
		t.bannered = true
		committed := t.sessionStartLocked()
		t.live.draw(committed, t.liveLinesLocked())
	}
	t.mu.Unlock()
	// the reader paints the live region, which the first draw above
	// established: it starts here (not at New), so a byte that lands
	// before the first Input (a pipe) cannot paint ahead of the banner.
	t.readerOnce.Do(func() { go t.readLoop() })
	defer func() {
		t.mu.Lock()
		t.reading = false
		t.mu.Unlock()
	}()
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if line, ok := t.takeSlot(); ok {
			if strings.TrimSpace(line) == "" {
				continue // a blank steering line: no-op, keep pulling
			}
			// a delivered slot line consumed the "interrupt landed"
			// fact (it is the line that broke the turn and became a
			// prompt), and starts the next turn.
			t.startTurnLocked(ctx)
			return line, nil
		}
		// a line the reader already pushed wins over a quit it outran
		// (the CLI's final-line rule, made deterministic: the pre-check
		// the slot gets, for the line — a select between a ready line
		// and a ready quit would be a coin).
		select {
		case line, ok := <-t.pending:
			if !ok {
				return "", io.EOF // the reader quit and nothing is queued
			}
			if out, delivered := t.consume(ctx, line); delivered {
				return out, nil
			}
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case line, ok := <-t.pending:
			if !ok {
				return "", io.EOF
			}
			if out, delivered := t.consume(ctx, line); delivered {
				return out, nil
			}
			// a blank line or a dispatched command: keep pulling.
		case <-t.wake:
			t.mu.Lock()
			quit := t.quit
			t.mu.Unlock()
			if quit {
				return "", io.EOF
			}
		}
	}
}

// consume runs one pushed line through the Input's dispatch and
// reports whether it delivered a prompt (true: the loop returns it) or
// consumed it (a blank no-op, a command's dispatch).
func (t *tui) consume(ctx context.Context, line string) (string, bool) {
	if strings.TrimSpace(line) == "" {
		return "", false // a blank line: no-op
	}
	if t.commands != nil && command.IsCommandLine(line) {
		t.dispatch(ctx, line)
		return "", false
	}
	out := line
	if strings.HasPrefix(line, "//") {
		out = command.Unescape(line) // the escape consumes one slash
	}
	// a line typed at a quiet prompt is a clean boundary: the
	// "interrupt landed" fact does not survive it (the CLI's).
	t.startTurnLocked(ctx)
	return out, true
}

// startTurnLocked is the turn start (under mu): the delivered line's
// ctx is the turn's, the activity row is put above the input row (the
// insertActivity op), the per-turn state is fresh.
func (t *tui) startTurnLocked(ctx context.Context) {
	t.mu.Lock()
	t.turnCtx = ctx
	t.steeredLive = false
	t.turnLive = true
	t.turnEstablished = false
	t.pend = nil // a new turn's pending line starts empty
	t.phase = "thinking"
	t.frame = 0
	t.toolName = ""
	t.toolArgs = nil
	t.live.insertActivity(t.activityLineLocked(), t.inputLineLocked())
	t.mu.Unlock()
}

// takeSlot pops the queued intent (one slot, latest wins).
func (t *tui) takeSlot() (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.hasSlot {
		return "", false
	}
	line := t.slot
	t.slot = ""
	t.hasSlot = false
	return line, true
}

// Notify observes one stream event and renders it (decision 2's commit
// points, exactly): the deltas as they arrive (reasoning dim, text
// normal), the tool block on ToolResult, the newline guarantee on
// Done, the fault line, the compact line plus the banner reprint, and
// the usage line on the explicit turn boundary. Events the TUI does
// not name are ignored (the compat rule; the CLI's discipline).
func (t *tui) Notify(ev core.Event) {
	switch ev.(type) {
	case core.ReasoningDelta, core.TextDelta, core.ToolStart, core.ToolResult,
		core.Done, core.Fault, core.Compacted, core.TurnEnd:
		// a named event: the live turn is established — a line that
		// lands from here on steers, not prompts (decision 9's
		// burst/steer boundary).
		t.mu.Lock()
		t.turnEstablished = true
		t.mu.Unlock()
	}
	switch e := ev.(type) {
	case core.ReasoningDelta:
		if e.Text == "" {
			return
		}
		t.mu.Lock()
		visible := t.showReasoning
		t.mu.Unlock()
		// the toggle governs subsequent reasoning (decision 5): hidden
		// deltas never touch the pending line, so the line's shape is
		// the CLI's minus them.
		if visible {
			t.flow(SlotReasoning, e.Text)
		}
	case core.TextDelta:
		if e.Text == "" {
			return
		}
		t.flow(SlotText, e.Text)
	case core.ToolStart:
		t.mu.Lock()
		t.toolName = e.Call.Name
		t.toolArgs = e.Call.Args
		t.phase = e.Call.Name // the activity row switches to the tool (decision 2)
		t.mu.Unlock()
		// the CLI's boundary byte: a newline before the row — it closes
		// the pending line, or stands as a blank line where none is open.
		t.flow("", "\n")
	case core.ToolResult:
		t.mu.Lock()
		block := RenderToolBlock(t.theme, t.toolName, t.toolArgs, e.Content, e.Err != nil, e.Duration)
		t.phase = "thinking"
		t.toolName = ""
		t.toolArgs = nil
		t.mu.Unlock()
		t.commit(block)
	case core.Done:
		// the CLI's newline, unconditional (the TUI adds, never changes,
		// the CLI's bytes): it closes the pending line, or stands as a
		// blank one where the text already ended on a line.
		t.mu.Lock()
		t.prompt += e.Usage.Prompt
		t.completion += e.Usage.Completion
		t.cacheRead += e.Usage.CacheRead
		t.mu.Unlock()
		t.flow("", "\n")
	case core.Fault:
		// the CLI's leading newline, then the fault line (its trailing
		// newline closes the line).
		t.mu.Lock()
		fault := RenderFault(t.theme, e.Err)
		t.mu.Unlock()
		t.flow("", "\n")
		t.commit(fault)
	case core.Compacted:
		// the compact line commits, then the banner reprints (decision
		// 3: the one moment the context number jumps).
		t.mu.Lock()
		chunk := RenderCompacted(t.theme, e) + "\n"
		if t.bannerIn != nil {
			chunk += RenderBanner(t.theme, t.bannerIn(context.Background()), t.width)
		}
		t.mu.Unlock()
		t.commit(chunk)
	case core.TurnEnd:
		// the explicit turn boundary (decision 2): a pending line that
		// never closed (an interrupted turn) commits as one, then the
		// usage line commits and the live region resets to the input
		// row alone.
		t.mu.Lock()
		pending := len(t.pend) > 0
		t.mu.Unlock()
		if pending {
			t.flow("", "\n")
		}
		t.mu.Lock()
		line := RenderUsage(t.theme, t.prompt, t.completion, t.cacheRead)
		t.prompt, t.completion, t.cacheRead = 0, 0, 0
		t.turnLive = false
		t.phase = "thinking"
		t.frame = 0
		t.pend = nil
		t.toolName = ""
		t.toolArgs = nil
		t.mu.Unlock()
		t.commit(line)
	default:
		// an unknown event: ignored (the compat rule), never misread.
	}
}

// commit flows one committed chunk over the live region and re-emits
// it (the draw op, live.go) — the shared door for every committed byte.
func (t *tui) commit(chunk string) {
	t.mu.Lock()
	t.live.draw(chunk, t.liveLinesLocked())
	t.mu.Unlock()
}

// liveLinesLocked is the live region's current content (under mu):
// the activity row, the pending line when one is open, and the input
// row while a turn is live (decision 1's at-most-three lines, decision
// 2's layout); the input row alone otherwise — including the TurnEnd
// reset, where the bookkeeping still carries the turn's rows but the
// new set is the input alone.
func (t *tui) liveLinesLocked() []string {
	if t.turnLive {
		lines := []string{t.activityLineLocked()}
		if pl := paintSegs(t.theme, t.pend); pl != "" {
			lines = append(lines, pl)
		}
		return append(lines, t.inputLineLocked())
	}
	return []string{t.inputLineLocked()}
}

// sessionStartLocked is the banner and the news line (under mu): the
// session-start reprint trigger (decision 3).
func (t *tui) sessionStartLocked() string {
	var b strings.Builder
	if t.bannerIn != nil {
		b.WriteString(RenderBanner(t.theme, t.bannerIn(context.Background()), t.width))
	}
	if t.news != nil {
		if line := t.news(context.Background()); line != "" {
			b.WriteString(t.theme.Paint(SlotDim, line))
		}
	}
	return b.String()
}

// dispatch consumes one command line (SPEC_COMMANDS 1, as the CLI's):
// the echo dim, the output committed as the CLI's bytes restyled by
// theme color (decision 5), an unknown command loud and naming the
// known set. The banner reprint triggers (decision 3) fire on success,
// exactly once: new, sessions resume, models switch.
func (t *tui) dispatch(ctx context.Context, line string) {
	// no separate echo: the committed prompt line is the echo (decision 5)
	name, args := command.Parse(line) // args: the raw remainder, the CLI's
	cmd, ok := t.commands[name]
	if !ok {
		t.commit(t.theme.Paint(SlotDim,
			"unknown command: "+name+" (known: "+strings.Join(t.known, ", ")+")"))
		return
	}
	out, err := cmd.Run(ctx, args, t.env)
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case err == nil && (name == "todo" || name == "scheduler") && out != "":
		// both doors (decision 6): the shared renderer, the door's own
		// opening line.
		opening := t.commandOpeningLocked(name, args)
		if name == "todo" {
			t.live.draw(RenderTodoBlock(t.theme, opening, out), t.liveLinesLocked())
		} else {
			t.live.draw(RenderSchedulerBlock(t.theme, opening, out), t.liveLinesLocked())
		}
		return
	case err != nil:
		t.live.draw(t.theme.Paint(SlotError, err.Error()), t.liveLinesLocked())
		return
	}
	reprint := false
	switch {
	case name == "new":
		reprint = true
	case name == "sessions" && strings.HasPrefix(args, "resume"):
		reprint = true
	case name == "models" && args != "":
		reprint = true
	}
	if out != "" {
		t.live.draw(t.theme.Paint(SlotText, out), t.liveLinesLocked())
	}
	if reprint && t.bannerIn != nil {
		t.live.draw(RenderBanner(t.theme, t.bannerIn(context.Background()), t.width), t.liveLinesLocked())
	}
}

// commandOpeningLocked is the command path's opening line (decision 6's
// both doors): the door's own line over the shared body.
func (t *tui) commandOpeningLocked(name, args string) string {
	s := t.theme.Paint(SlotAccent, "/"+name)
	if args != "" {
		s += t.theme.Paint(SlotDim, " · ") + t.theme.Paint(SlotText, args)
	}
	return s
}

// --- the Steerer (SPEC_COMMANDS 2): the frontend-owned seam ----------

// Steer queues the text (latest wins) and reports whether an interrupt
// landed: the live turn now (a dispatch from a keypress mid-turn), or
// the reader's "an interrupt just landed" fact (consumed here).
func (t *tui) Steer(text string) bool {
	t.mu.Lock()
	t.slot = text
	t.hasSlot = true
	live := t.turnLive
	wasLive := t.steeredLive
	t.steeredLive = false
	cancel := t.cancel
	t.mu.Unlock()
	if live && cancel != nil {
		cancel()
		return true
	}
	return wasLive
}

// Interrupt interrupts a live turn only — no text is queued — and
// reports the same way Steer does.
func (t *tui) Interrupt() bool {
	t.mu.Lock()
	live := t.turnLive
	wasLive := t.steeredLive
	t.steeredLive = false
	cancel := t.cancel
	t.mu.Unlock()
	if live && cancel != nil {
		cancel()
		return true
	}
	return wasLive
}

// ClearSlot drops the queued intent (SPEC_COMMANDS 4: a new session
// does not inherit it).
func (t *tui) ClearSlot() {
	t.mu.Lock()
	t.slot = ""
	t.hasSlot = false
	t.mu.Unlock()
}

// LiveTurn: a turn is live right now (the compact / new / sessions
// resume refusal, SPEC_COMMANDS 3).
// LiveTurn reports the turn's state (the Steerer's fact): true from
// the line's Enter (or delivery) until the turn's end — turnLive, the
// flag the delivery and the TurnEnd keep in lockstep (the turn's ctx
// outlives the turn and is not the state).
func (t *tui) LiveTurn() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.turnLive
}

// --- the live region's dynamic lines ---------------------------------

// activityLineLocked is the activity row (under mu): the spinner's
// frame (decision 8's |/-\, four frames) and the current phase —
// thinking dim, a tool name in the accent.
func (t *tui) activityLineLocked() string {
	frame := "|/-\\"[t.frame%4]
	label := t.phase
	if label == "" {
		label = "thinking"
	}
	line := t.theme.Paint(SlotDim, string(frame))
	if label == "thinking" {
		line += t.theme.Paint(SlotDim, " thinking")
	} else {
		line += t.theme.Paint(SlotDim, " ") + t.theme.Paint(SlotAccent, label)
	}
	return line
}

// hintLocked is the inline hint for a command line being typed
// (decision 9): fish-style ghost text after the cursor, display only,
// never part of the buffer. While the name is being typed: the first
// match's remainder as a ghost, plus the other candidates dim. After a
// known name and a space: the command's description, dim. Empty when
// the line is not a command shape (plain prompts, the // escape).
func (t *tui) hintLocked() string {
	text := t.inputText
	if t.commands == nil || !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "//") {
		return ""
	}
	rest := text[1:]
	if sp := strings.IndexAny(rest, " \t"); sp >= 0 {
		name := rest[:sp]
		if cmd, ok := t.commands[name]; ok {
			return cmd.Description()
		}
		return ""
	}
	matches := t.matchesLocked(rest)
	if len(matches) == 0 {
		return ""
	}
	ghost := strings.TrimPrefix(matches[0], rest)
	if len(matches) == 1 {
		return ghost
	}
	return ghost + "  · " + strings.Join(matches, " ")
}

// matchesLocked: the known command names with the prefix, sorted (the
// known list already is).
func (t *tui) matchesLocked(prefix string) []string {
	var out []string
	for _, name := range t.known {
		if strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	return out
}

// completeCommand lands the tab (decision 9): the buffer becomes the
// longest common prefix of the matches, plus the trailing space when
// unique. A non-command line, no matches, or no progress: a no-op.
func (t *tui) completeCommand() {
	t.mu.Lock()
	text := t.ed.text()
	if t.commands == nil || !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "//") || strings.ContainsAny(text, " \t") {
		t.mu.Unlock()
		return
	}
	matches := t.matchesLocked(text[1:])
	t.mu.Unlock()
	if len(matches) == 0 {
		return
	}
	lcp := matches[0]
	for _, m := range matches[1:] {
		for !strings.HasPrefix(m, lcp) {
			lcp = lcp[:len(lcp)-1]
		}
	}
	next := "/" + lcp
	if len(matches) == 1 {
		next += " "
	}
	if next == text {
		return
	}
	t.ed.setText(next)
}

// inputLineLocked is the painted input row (under mu).
func (t *tui) inputLineLocked() string {
	line, _ := t.inputLineAndColLocked()
	return line
}

// inputLineAndColLocked is the painted input row (under mu) and the
// edit column over it. The line's budget leaves one column of headroom
// under the width (the cursor's, the right edge's); the budget is the
// text's, after the prompt and its space. A line longer than the
// budget (a paste, a shrunken width) shows its prefix plus the dot —
// the Enter commits the full text (the terminal wraps committed prose,
// decision 8).
func (t *tui) inputLineAndColLocked() (string, int) {
	glyph := t.theme.Glyph(GlyphPrompt)
	prefixCols := displayWidth(glyph) + 1
	text := t.inputText
	budget := t.width - 3
	if budget < 1 {
		budget = 1
	}
	visible := text
	if displayWidth(text) > budget {
		visible = truncateWidth(t.theme, text, budget)
	}
	line := t.theme.Paint(SlotAccent, glyph) +
		t.theme.Paint(SlotText, " "+visible)
	// the inline hint (decision 9): ghost text after the typed text, dim,
	// display only. Only when the text fits (a truncated line has no
	// room) and the cursor sits at the end (mid-line edits keep the row
	// clean); truncated to what the width leaves.
	if t.editPos == len([]rune(text)) && visible == text {
		if hint := t.hintLocked(); hint != "" {
			room := t.width - prefixCols - displayWidth(text) - 2
			if room > 1 {
				if displayWidth(hint) > room {
					hint = truncateWidth(t.theme, hint, room)
				}
				line += t.theme.Paint(SlotDim, hint)
			}
		}
	}
	col := prefixCols + visiblePrefixWidth(text, t.editPos, budget) + 1
	return line, col
}

// visiblePrefixWidth is the display width of the text's prefix up to
// pos, capped at the visible budget (the edit position past the
// visible prefix parks the cursor at the visible line's end).
func visiblePrefixWidth(s string, pos, cap int) int {
	w := 0
	for i, r := range s {
		if i >= pos {
			break
		}
		w += runeWidth(r)
		if w > cap {
			return cap
		}
	}
	return w
}

// paintLines paints a multi-line text per line (the walk's chunk
// contract, live.go): a paint's SGR and reset never span a newline.
func paintLines(t Theme, slot, text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = t.Paint(slot, l)
	}
	return strings.Join(lines, "\n")
}

// seg is one piece of the pending line: the streamed text and the slot
// it arrived with. A line can carry several — the CLI concatenates the
// bytes raw (reasoning, then text, no separator of its own), and the
// TUI restyles each piece in its own slot.
type seg struct {
	slot string
	text string
}

// paintSegs paints the pending line's segments in arrival order (the
// CLI's raw concatenation, restyled).
func paintSegs(t Theme, segs []seg) string {
	var b strings.Builder
	for _, s := range segs {
		if s.slot == "" {
			b.WriteString(s.text)
			continue
		}
		b.WriteString(t.Paint(s.slot, s.text))
	}
	return b.String()
}

// flow is the streaming pass (decision 2): the delta lands on the
// pending line, and the lines a newline closed commit as flowed —
// the terminal wraps them, rig never hand-wraps (decision 8). A bare
// newline (the CLI's boundaries: ToolStart, Done, Fault) closes the
// pending line, or commits a blank one where the line is empty — the
// CLI's bytes, kept verbatim.
func (t *tui) flow(slot, text string) {
	t.mu.Lock()
	t.pend = append(t.pend, seg{slot: slot, text: text})
	lines := t.takeClosedLinesLocked()
	chunk := ""
	if len(lines) > 0 {
		chunk = strings.Join(lines, "\n") + "\n"
	}
	t.live.draw(chunk, t.liveLinesLocked())
	t.mu.Unlock()
}

// takeClosedLinesLocked splits the pending line on the newlines its
// segments carry, resets it to the remainder, and returns the closed
// lines painted (under mu). An empty closed line is a blank line — the
// CLI's boundary bytes are the render.
func (t *tui) takeClosedLinesLocked() []string {
	var lines []string
	var cur []seg
	for _, s := range t.pend {
		parts := strings.Split(s.text, "\n")
		for i, p := range parts {
			if i < len(parts)-1 {
				if p != "" {
					cur = append(cur, seg{slot: s.slot, text: p})
				}
				lines = append(lines, paintSegs(t.theme, cur))
				cur = nil
			} else if p != "" {
				cur = append(cur, seg{slot: s.slot, text: p})
			}
		}
	}
	t.pend = cur
	return lines
}

// --- the dynamic loops ------------------------------------------------

// tickLoop advances the spinner's frame and rewrites the activity row
// in place (the setActivity op) — while a turn is live and the
// activity row is the bookkeeping's first (the steering window, where
// it is not, ticks nothing).
func (t *tui) tickLoop() {
	for {
		select {
		case <-t.closed:
			return
		case <-t.ticks:
			t.mu.Lock()
			if t.turnLive && len(t.live.lines) == 2 {
				t.frame++
				t.live.setActivity(t.activityLineLocked())
			}
			t.mu.Unlock()
		}
	}
}

// winchLoop repaints the live region on resize (decision 2: history is
// the terminal's problem, already solved) and takes the new width.
func (t *tui) winchLoop() {
	for {
		select {
		case <-t.closed:
			return
		case <-t.winch:
			t.mu.Lock()
			if t.fdi != 0 {
				if w, _, err := term.GetSize(t.fdi); err == nil && w > 0 {
					t.width = w
					t.live.setWidth(w)
				}
			}
			if len(t.live.lines) > 0 {
				t.live.draw("", t.liveLinesLocked())
			}
			t.mu.Unlock()
		}
	}
}
