package tui

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
)

// tui is the terminal Frontend (SPEC_TUI): the reader goroutine, the
// command dispatch, and the status line's refresh points, over the
// pure renderers (decisions 3, 5, 6) and the live-region protocol
// (decision 2). The runtime contract is the CLI's: one slot, latest
// wins; the steering and Ctrl-C semantics unchanged (decision 9); the
// loop's contract "Input returns one message" a prompt, never a
// command.
type tui struct {
	theme Theme
	in    io.Reader
	fdi   int // the input fd: raw mode and the size (0 = not a tty)

	mu            sync.Mutex
	width         int
	height        int // the terminal's height: the pager's budget
	pg            *pager
	fromPager     bool // reader-owned: a key forwarded by the pager to the editor (no re-entry)
	live          *live
	inputText     string // the line's text, rendered under mu
	editPos       int    // the edit position, mirrored under mu
	inputScroll   int    // the input window's top row (a >5-row text scrolls with the cursor)
	phase         string // the activity line's label: thinking, or a tool
	frame         int    // the spinner's frame (|/-\)
	showReasoning bool
	turnLive      bool
	compacting    bool // the summary call is running outside a live turn (the verb's loader)
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
	started         bool // the session start (the startup block) has committed
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
	// markdown: the decoration pass is on (the default; a later switch
	// may turn it off). codeMode: inside a fenced block (the pass
	// toggles it): lines commit preformatted, never word-wrapped, never
	// decorated.
	markdown bool
	codeMode bool
	codeLang string // the fence's language (langOf the info string) while in code mode
	// lastSlot: the slot of the last streamed text (decision 2's
	// spacing rule): a reasoning block that ends and text or a tool
	// that begins get one blank row between them, whether or not the
	// model emitted a newline there; and after a tool block, whatever
	// streams next starts a fresh paragraph (slotAfterTool).
	lastSlot string

	statusIn func(context.Context) StatusIn
	news     func(context.Context) string
	commands map[string]core.Command
	known    []string
	env      any

	// the completion menu (decision 9, amended): the current
	// candidates, the selection, and the Esc-closed flag (the close
	// lives until the input changes).
	menuCands []menuCand
	menuSel   int
	menuDead  bool
	// the status line (decision 3): the snapshot from the root's
	// closure (the banner's old door, refreshed at its reprint points)
	// and the used number from the usage events (the last Done's
	// Prompt + Completion, or the compact's Kept).
	statusModel   string
	statusEffort  string
	statusWindow  int
	statusUsed    int
	statusHasUsed bool
	// the status's second row: the last turn's usage (TurnEnd sets it
	// from the turn's totals); the session's totals from the snapshot
	// before the first turn.
	statusUp, statusDown, statusCache int

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

	// sizeOf reads the terminal's size (the production default is
	// term.GetSize on the input tty; the tests inject). Repaints call
	// it before building rows: a resize's SIGWINCH is asynchronous,
	// and a delta that repaints between the resize and the signal
	// would clear the region with a stale width — the reflowed rows
	// on screen and the bookkeeping disagree, and every repaint leaves
	// the region's top rows behind (the duplicate cascade, found on a
	// two-client tmux).
	sizeOf func() (int, int, bool)
}

// WithSize is the size seam (a test seam: the tests inject a size;
// the production default reads the input tty).
func WithSize(f func() (int, int, bool)) Option {
	return func(t *tui) { t.sizeOf = f }
}

// syncSizeLocked re-reads the terminal size before a repaint builds
// rows (under mu): a change lands in the width, the height, and the
// live region's wrap math — ahead of the SIGWINCH, which still fires
// for the full repaint.
func (t *tui) syncSizeLocked() {
	if t.sizeOf == nil {
		return
	}
	w, h, ok := t.sizeOf()
	if !ok || w <= 0 {
		return
	}
	if w != t.width || h != t.height {
		t.width, t.height = w, h
		t.live.setWidth(w)
	}
}

// Option configures the frontend.
type Option func(*tui)

// WithTheme is the resolved palette (decision 7's selection, the
// root's ResolveTheme over settings.theme and theme.json).
func WithTheme(t Theme) Option { return func(tu *tui) { tu.theme = t } }

// WithWidth is the terminal's width (the production default is the tty
// size at start; the tests name it).
func WithWidth(w int) Option { return func(t *tui) { t.width = w } }

// WithStatus is the status line's and the startup block's numbers
// (decision 3, amended): the root computes them at call time (the
// session start, and the refresh points: new, sessions resume, a
// models switch) — a store read, never per repaint.
func WithStatus(f func(context.Context) StatusIn) Option {
	return func(t *tui) { t.statusIn = f }
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
// Steer seam is the frontend's, filled here (as the CLI's); the
// models' Sub() hints take their names from the Env here too (the
// menu is the TUI's, the hints the command package's).
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
			command.ModelHints(cmds, e)
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
		height:        24,
		showReasoning: true,
		markdown:      true,
		ed:            newEditor(),
		pending:       make(chan string, 16),
		wake:          make(chan struct{}, 1),
		closed:        make(chan struct{}),
	}
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		t.fdi = int(f.Fd())
		if w, h, err := term.GetSize(t.fdi); err == nil && w > 0 {
			t.width = w
			t.height = h
		}
		if old, err := term.MakeRaw(t.fdi); err == nil {
			t.rawOld = old
		}
		fdi := t.fdi
		t.sizeOf = func() (int, int, bool) {
			w, h, err := term.GetSize(fdi)
			return w, h, err == nil
		}
	}
	for _, opt := range opts {
		opt(t)
	}
	t.live = newLive(out, t.width)
	t.live.onSuspended = t.repaintPagerLocked // the pager follows the region while it is up
	if t.rawOld != nil {
		// bracketed paste (decision 9): a paste arrives as one input,
		// its newlines text; only a real tty gets the mode (and Close
		// puts it back).
		io.WriteString(out, pasteOn)
	}
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
	t.mu.Lock()
	if t.pg != nil {
		// a pager left open would strand the terminal on the alt
		// screen; the way out closes it (the resume repaint is moot —
		// the process is leaving).
		t.pg = nil
		io.WriteString(t.live.w, altOff)
	}
	t.mu.Unlock()
	if t.rawOld != nil {
		io.WriteString(t.live.w, pasteOff)
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
	// the pump: the blocking reads in their own goroutine, so the loop
	// can time out on a half-open escape. The close is the EOF, and the
	// buffered bytes drain before it delivers.
	bytes := make(chan byte, 64)
	go func() {
		br := bufio.NewReader(t.in)
		for {
			b, err := br.ReadByte()
			if err != nil {
				close(bytes)
				return
			}
			bytes <- b
		}
	}()
	for {
		// a lone Esc: the escape families arrive as one burst, so an
		// ESC with nothing behind it inside the grace window is the key
		// itself, not a sequence opening (readline's disambiguation; the
		// parser cannot name it byte-at-a-time).
		var esc <-chan time.Time
		if t.kp.state == stEsc {
			esc = time.After(escDelay)
		}
		select {
		case b, ok := <-bytes:
			if !ok {
				t.onInputEOF()
				return
			}
			if k, r := t.kp.next(b); k != keyNone {
				t.onKey(k, r)
			}
		case <-esc:
			t.kp.state = stTop
			t.onKey(keyEsc, 0)
		}
	}
}

// escDelay is the lone-Esc grace window: a sequence's bytes arrive
// together (the terminal writes them in one burst), so a silent gap
// after an ESC is the key itself.
const escDelay = 30 * time.Millisecond

// onKey applies one parsed key to the line (the editor) and repaints;
// the named control keys do their thing (decision 9). Inside the
// pager, the keys are the pager's.
func (t *tui) onKey(k key, r rune) {
	if !t.fromPager && t.pagerKey(k, r) {
		return
	}
	switch k {
	case keyPgUp:
		t.enterPager()
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
	case keyEsc:
		// Esc's precedence (decision 9, amended): the pager first
		// (pagerKey above), then the menu (the input keeps its text),
		// then the prompt clear.
		if t.closeMenu() {
			return
		}
		t.ed.apply(keyEsc, 0)
		t.mu.Lock()
		t.menuSyncLocked()
		t.mu.Unlock()
		t.paintInput()
	case keyTab:
		// Tab (decision 9, amended): the menu's selection steps down
		// while it is open; a single candidate is completed with its
		// trailing space; anywhere else, a no-op.
		t.mu.Lock()
		next, changed := t.tabTextLocked()
		t.mu.Unlock()
		if changed {
			t.ed.setText(next)
			t.mu.Lock()
			t.menuSyncLocked()
			t.mu.Unlock()
		}
		t.paintInput()
	case keyShiftTab:
		// Shift-Tab (CSI Z): the menu's selection steps up.
		t.mu.Lock()
		if t.menuOpenLocked() {
			n := len(t.menuCands)
			t.menuSel = (t.menuSel - 1 + n) % n
		}
		t.mu.Unlock()
		t.paintInput()
	default:
		before := t.ed.text()
		t.ed.apply(k, r)
		if t.ed.text() != before {
			t.mu.Lock()
			t.menuSyncLocked()
			t.mu.Unlock()
		}
		t.paintInput()
	}
}

// menuSyncLocked refreshes the completion for the input's current text
// (under mu; decision 9, amended): a new text resets the selection and
// reopens the menu (Esc's close lives until the text changes).
func (t *tui) menuSyncLocked() {
	t.menuCands, _, _ = t.completionLocked()
	t.menuSel = 0
	t.menuDead = false
}

// menuOpenLocked: the menu is showing (two or more candidates, not
// Esc-closed).
func (t *tui) menuOpenLocked() bool {
	return len(t.menuCands) >= 2 && !t.menuDead
}

// closeMenu closes the menu if it is open (Esc's middle rung) and
// reports whether it was.
func (t *tui) closeMenu() bool {
	t.mu.Lock()
	open := t.menuOpenLocked()
	if open {
		t.menuDead = true
	}
	t.mu.Unlock()
	if open {
		t.paintInput()
	}
	return open
}

// pagerKey routes a key while the pager is open (the copy-mode's
// vocabulary); it reports whether the key was the pager's. Every key
// it does not name is consumed too — the editor must not move under a
// modal screen.
func (t *tui) pagerKey(k key, r rune) bool {
	t.mu.Lock()
	if t.pg == nil {
		t.mu.Unlock()
		return false
	}
	empty := strings.TrimSpace(t.ed.text()) == ""
	moved := false
	switch {
	case k == keyEsc, k == keyText && (r == 'q' || r == 'Q') && empty, k == keyEnter && empty:
		// the way out: Esc always; q and an empty Enter when nothing
		// is typed (typing q into a prompt is a letter).
		t.exitPagerLocked()
		t.mu.Unlock()
		return true
	case k == keyPgUp:
		moved = t.pg.move(t.pg.page())
	case k == keyPgDn:
		moved = t.pg.move(-t.pg.page())
	case k == keyHome && empty:
		moved = t.pg.move(len(t.pg.lines))
	case k == keyEnd && empty:
		moved = t.pg.move(-len(t.pg.lines))
	case k == keyUp && empty:
		moved = t.pg.move(1)
	case k == keyDown && empty:
		moved = t.pg.move(-1)
	case k == keyCtrlC:
		// the session end works from anywhere; the pager closes first
		// so the terminal is sane on the way out.
		t.exitPagerLocked()
		t.mu.Unlock()
		t.quitSession()
		return true
	default:
		// everything else is the editor's (the amended pager: the
		// operator types and steers while the history scrolls above).
		// Enter with text submits and returns to the main screen, so
		// the turn is watched there.
		t.mu.Unlock()
		if k == keyEnter {
			t.mu.Lock()
			t.exitPagerLocked()
			t.mu.Unlock()
			t.onEnter()
			return true
		}
		t.fromPager = true
		t.onKey(k, r)
		t.fromPager = false
		return true
	}
	if moved {
		t.pg.render(t.live.w, t.theme)
	}
	t.mu.Unlock()
	return true
}

// enterPager opens the copy-mode: the live region suspends (its
// bookkeeping runs on, its writes stop), the alt screen opens, the
// history renders.
func (t *tui) enterPager() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pg != nil {
		return
	}
	t.pg = newPager(t.live.hist, t.width, t.height)
	t.pg.footer = t.footerLocked()
	t.live.suspend()
	io.WriteString(t.live.w, altOn)
	t.pg.render(t.live.w, t.theme)
}

// footerLocked is the live region's rows for the pager's footer (under
// mu): the region as liveLinesLocked lays it out, then the status rows.
func (t *tui) footerLocked() []string {
	rows := t.liveLinesLocked()
	return append(rows, statusRows(t.statusLineLocked())...)
}

// repaintPagerLocked refreshes the pager (under mu) when the live
// region would have repainted: the history may have grown (a commit
// queued while paging is in hist already), and the footer follows the
// region (a tick, a keystroke, a delta).
func (t *tui) repaintPagerLocked() {
	if t.pg == nil {
		return
	}
	t.pg.lines = t.live.hist
	t.pg.footer = t.footerLocked()
	t.pg.clamp()
	t.pg.render(t.live.w, t.theme)
}

// exitPagerLocked closes the copy-mode: the alt screen closes (the
// terminal restores the main screen itself), and the live region
// resumes — replaying what committed while the pager was up.
func (t *tui) exitPagerLocked() {
	if t.pg == nil {
		return
	}
	t.pg = nil
	io.WriteString(t.live.w, altOff)
	t.live.resume()
}

// paintInput rewrites the input row and parks the terminal cursor at
// the edit column (the edit op, live.go). The menu rows and the status
// row (decision 2's layout) are live lines: while their shape stands,
// the edit op's in-place rewrite leaves them where they are; a shape
// change (the menu opens, closes, or its selection moves) re-lays the
// whole region (the editFull op).
func (t *tui) paintInput() {
	t.mu.Lock()
	t.inputText = t.ed.text()
	t.editPos = t.ed.pos
	line, col := t.inputLineAndColLocked()
	status := t.statusLineLocked()
	lines := t.liveLinesLocked()
	if !t.regionStableLocked(lines, status) {
		t.live.editFull(lines, col, status)
	} else {
		t.live.edit(line, col, status)
	}
	t.mu.Unlock()
}

// regionStableLocked: the region's non-input rows (the activity, the
// pending line, the menu rows, and the status row's presence) are the
// same the last paint left on screen (under mu), and the input row's
// terminal row count stands — the edit op's in-place rewrite covers
// this keystroke. A row count change would push the status row (the
// region's last) to a new row, which only the full re-layout (the
// editFull op) places.
func (t *tui) regionStableLocked(lines []string, status string) bool {
	old := t.live.lines
	oldOffset := 0
	if t.live.status != "" {
		oldOffset = 1
	}
	if len(old) < 1+oldOffset || (status != "") != (oldOffset != 0) {
		return false
	}
	oldIn := old[len(old)-1-oldOffset]
	newIn := lines[len(lines)-1]
	if t.live.visualRows(oldIn) != t.live.visualRows(newIn) {
		return false
	}
	oldPart := old[:len(old)-1-oldOffset]
	newPart := lines[:len(lines)-1]
	if len(oldPart) != len(newPart) {
		return false
	}
	for i := range oldPart {
		if oldPart[i] != newPart[i] {
			return false
		}
	}
	return true
}

// onEnter submits the line (the editor consumes it): the Enter path
// (live.go) freezes it, and the routing is the CLI's — a live turn
// takes the slot and the interrupt; a quiet prompt delivers in order.
func (t *tui) onEnter() {
	t.mu.Lock()
	// the menu's accept (decision 9, amended): the selection fills the
	// input — the typed prefix replaced by the candidate plus a
	// trailing space — and never dispatches.
	if t.menuOpenLocked() {
		accept := t.menuAcceptLocked()
		t.mu.Unlock()
		t.ed.setText(accept)
		t.mu.Lock()
		t.menuSyncLocked()
		t.mu.Unlock()
		t.paintInput()
		return
	}
	// the ghost's Enter (decision 9, amended): a single candidate's
	// remainder is showing, so the visible line is the intent — the
	// completion lands first (Tab's text), and the Enter submits it.
	// The typed prefix alone would dispatch as an unknown command
	// while the row promised the completion.
	if next, ok := t.tabTextLocked(); ok {
		t.mu.Unlock()
		t.ed.setText(next)
		t.mu.Lock()
	}
	t.mu.Unlock()
	line, submitted := t.ed.apply(keyEnter, 0)
	if !submitted || strings.TrimSpace(line) == "" {
		return // a blank line is a no-op, consumed (the CLI's rule)
	}
	t.mu.Lock()
	t.syncSizeLocked()
	// the prompt line soft-wraps at words like any prose (decision 2,
	// amended); the rows commit as one frozen block, the glyph on the
	// first row.
	promptRows := wrapSegs(t.theme, t.width, []seg{
		{slot: SlotEmber, text: t.theme.Glyph(GlyphPrompt)},
		{slot: SlotText, text: " " + displayInput(line)},
	})
	full := strings.Join(promptRows, "\n")
	t.inputText = ""
	t.editPos = 0
	t.menuSyncLocked()
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
	t.live.enter(full, "", t.inputLineLocked(), t.statusLineLocked())
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
	if !t.started {
		// the session start: the startup block, committed exactly once
		// (decision 3, amended: the refresh points are dispatch, and
		// the status row takes the usage events).
		t.started = true
		committed := t.sessionStartLocked()
		t.live.draw(committed, t.liveLinesLocked(), t.statusLineLocked())
	}
	t.mu.Unlock()
	// the reader paints the live region, which the first draw above
	// established: it starts here (not at New), so a byte that lands
	// before the first Input (a pipe) cannot paint ahead of the startup
	// block.
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
	t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
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
// Done, the fault line, the compact line (and the status line's used
// taking the Kept), and the usage line on the explicit turn boundary.
// Events the TUI does not name are ignored (the compat rule; the
// CLI's discipline).
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
		// The slot is text (a paint-free newline): the reasoning
		// boundary rule sees a tool as "something else".
		t.flow(SlotText, "\n")
	case core.ToolResult:
		t.mu.Lock()
		block := RenderToolBlock(t.theme, t.toolName, t.toolArgs, e.Content, e.Err != nil, e.Duration)
		t.phase = "thinking"
		t.toolName = ""
		t.toolArgs = nil
		t.lastSlot = slotAfterTool // the next stream starts a fresh paragraph
		t.mu.Unlock()
		t.commit(block)
	case core.Done:
		// the CLI's newline, unconditional (the TUI adds, never changes,
		// the CLI's bytes): it closes the pending line, or stands as a
		// blank one where the text already ended on a line. The status
		// row takes the usage (decision 3, amended): the last
		// assistant message's context anchor, exactly.
		t.mu.Lock()
		t.prompt += e.Usage.Prompt
		t.completion += e.Usage.Completion
		t.cacheRead += e.Usage.CacheRead
		if e.Usage.Prompt > 0 || e.Usage.Completion > 0 {
			t.statusUsed = e.Usage.Prompt + e.Usage.Completion
			t.statusHasUsed = true
		}
		// the usage row is live within the turn (decision 3): each
		// model call's Done moves it, so a long agentic turn shows its
		// running up/down and hit rate, not the previous turn's until
		// the close.
		t.statusUp, t.statusDown, t.statusCache = t.prompt, t.completion, t.cacheRead
		t.mu.Unlock()
		t.flow("", "\n")
	case core.Fault:
		// the CLI's leading newline, then the fault line (its trailing
		// newline closes the line). A failed summary must not strand
		// the verb's loader row.
		t.mu.Lock()
		t.compacting = false
		fault := RenderFault(t.theme, e.Err)
		t.mu.Unlock()
		t.flow("", "\n")
		t.commit(fault)
	case core.Compacting:
		// the loader (SPEC_COMPACT 5, amended): the summary call may
		// prefill for minutes. In a live turn the phase changes; on the
		// verb's door the activity row is placed for the duration.
		t.mu.Lock()
		t.phase = "compacting"
		t.frame = 0
		if !t.turnLive {
			t.compacting = true
		}
		// the region repaint (the TUI owns the layout; the activity row
		// sits wherever liveLinesLocked places it).
		if len(t.live.lines) > 0 {
			t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
		}
		t.mu.Unlock()
	case core.Compacted:
		// the compact line commits, and the status row takes the
		// compact's Kept (decision 3, amended: the banner's reprint is
		// the status line's update, no block at all). The verb's loader
		// row leaves with the commit (liveLines drops it).
		t.mu.Lock()
		t.compacting = false
		if t.turnLive {
			t.phase = "thinking"
		}
		chunk := RenderCompacted(t.theme, e) + "\n"
		if e.Kept > 0 {
			t.statusUsed = e.Kept
			t.statusHasUsed = true
		}
		t.mu.Unlock()
		t.commit(chunk)
	case core.TurnEnd:
		// the explicit turn boundary (decision 2): a pending line that
		// never closed (an interrupted turn) commits as one, then the
		// usage line commits and the live region resets to the input
		// row alone.
		t.mu.Lock()
		t.lastSlot = ""
		t.codeMode, t.codeLang = false, "" // an unclosed fence does not leak into the next turn
		pending := len(t.pend) > 0
		t.mu.Unlock()
		if pending {
			t.flow("", "\n")
		}
		t.mu.Lock()
		// the turn's usage goes to the status's second row (decision 3,
		// amended: no committed usage line — the numbers live under
		// the input, once, not in the scrollback per turn).
		t.statusUp, t.statusDown, t.statusCache = t.prompt, t.completion, t.cacheRead
		t.prompt, t.completion, t.cacheRead = 0, 0, 0
		t.turnLive = false
		t.phase = "thinking"
		t.frame = 0
		t.pend = nil
		t.toolName = ""
		t.toolArgs = nil
		t.mu.Unlock()
		t.commit("")
	default:
		// an unknown event: ignored (the compat rule), never misread.
	}
}

// commit flows one committed chunk over the live region and re-emits
// it (the draw op, live.go) — the shared door for every committed byte.
func (t *tui) commit(chunk string) {
	t.mu.Lock()
	t.live.draw(chunk, t.liveLinesLocked(), t.statusLineLocked())
	t.mu.Unlock()
}

// liveLinesLocked is the live region's current content, above the
// status row (under mu; decision 2's layout, amended): the activity
// row, the pending line when one is open, the completion menu when it
// is showing (decision 9, amended), and the input row — the status
// row is the region's last row, passed to the ops separately.
func (t *tui) liveLinesLocked() []string {
	t.syncSizeLocked()
	// the margins (decision 2, amended): one blank live row between
	// the region's groups — the loader stands apart from the streamed
	// text and from the input, and the input stands apart from
	// everything above it. The blank rows are live rows: they leave
	// with the region, never committed (the enter op commits the prompt
	// line alone, and the spacing rule's collapse keeps the scrollback
	// to one blank between blocks).
	var lines []string
	if t.turnLive || t.compacting {
		// the pending line first, the activity row under it (decision
		// 2, amended): the loader locks above the input; streamed text
		// flows into scrollback above the loader, never under it.
		if pl := paintSegs(t.theme, t.pend); pl != "" {
			lines = append(lines, pl, "")
		} else if !t.live.lastBlank {
			// no pending line: the loader's margin is a live blank
			// unless the scrollback already ends with one.
			lines = append(lines, "")
		}
		lines = append(lines, t.activityLineLocked())
	}
	// the margin above the input (or above the menu when it is open):
	// a live blank row — unless nothing live stands above it AND the
	// last committed row is already a blank (the Done newline, the
	// prompt's own margin), so the transcript never shows two. With
	// the loader above, the margin separates the loader from the
	// input whatever the scrollback ends with.
	if len(lines) > 0 || !t.live.lastBlank {
		lines = append(lines, "")
	}
	if ml := t.menuLinesLocked(); len(ml) > 0 {
		lines = append(lines, ml...)
	}
	return append(lines, t.inputLineLocked())
}

// statusLineLocked is the status row (under mu; decision 3): the
// model, and used over the window once a turn has run.
func (t *tui) statusLineLocked() string {
	st := RenderStatusLine(t.theme, t.statusModel, t.statusUsed, t.statusWindow, t.statusHasUsed,
		t.statusUp, t.statusDown, t.statusCache)
	if st == "" {
		return ""
	}
	// the margin under the input (decision 2, amended): a blank status
	// row above the model row, so the input stands apart from the
	// numbers.
	return "\n" + st
}

// sessionStartLocked is the startup block and the status line's
// snapshot (under mu): the session start (decision 3, amended).
func (t *tui) sessionStartLocked() string {
	var b strings.Builder
	if t.statusIn != nil {
		in := t.statusIn(context.Background())
		t.statusModel = in.Model
		t.statusEffort = in.Effort
		t.statusWindow = in.Window
		t.statusUsed = 0
		t.statusHasUsed = false
		t.statusUp, t.statusDown, t.statusCache = in.Up, in.Down, in.CacheRead
		b.WriteString(RenderStatus(t.theme, in))
	}
	if t.news != nil {
		if line := t.news(context.Background()); line != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(t.theme.Paint(SlotDim, line))
		}
	}
	return b.String()
}

// dispatch consumes one command line (SPEC_COMMANDS 1, as the CLI's):
// the echo dim, the output committed as the CLI's bytes restyled by
// theme color (decision 5), an unknown command loud and naming the
// known set. The status line's refresh points (decision 3, amended —
// the banner's old reprint triggers) fire on success, exactly once:
// new, sessions resume, models switch.
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
			t.live.draw(RenderTodoBlock(t.theme, opening, out), t.liveLinesLocked(), t.statusLineLocked())
		} else {
			t.live.draw(RenderSchedulerBlock(t.theme, opening, out), t.liveLinesLocked(), t.statusLineLocked())
		}
		return
	case err != nil:
		t.live.draw(t.theme.Paint(SlotError, err.Error()), t.liveLinesLocked(), t.statusLineLocked())
		return
	}
	refresh := false
	fresh := false
	switch {
	case name == "new":
		refresh, fresh = true, true
	case name == "sessions" && strings.HasPrefix(args, "resume"):
		refresh, fresh = true, true
	case name == "models" && args != "":
		refresh = true // the same context under a new window: the used number stands
	}
	if out != "" {
		t.live.draw(t.theme.Paint(SlotText, out), t.liveLinesLocked(), t.statusLineLocked())
	}
	if refresh && t.statusIn != nil {
		// the snapshot refresh (decision 3, amended): the banner's
		// reprint trigger, now the live row's data update; new and
		// resume start fresh contexts, the used number resets with
		// the session.
		in := t.statusIn(context.Background())
		t.statusModel = in.Model
		t.statusEffort = in.Effort
		t.statusWindow = in.Window
		if fresh {
			t.statusUsed = 0
			t.statusHasUsed = false
		}
		t.statusUp, t.statusDown, t.statusCache = in.Up, in.Down, in.CacheRead
		t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
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
	// the loader is the ember (decision 7, amended): the spinner and
	// its label, whatever the phase (thinking, compacting, a tool).
	return t.theme.Paint(SlotEmber, string(frame)+" "+label)
}

// menuCand is one completion candidate (decision 9, amended): the
// name the operator can type, and its description (the menu row's
// second half).
type menuCand struct {
	name string
	desc string
}

// completionLocked is the current completion (under mu; decision 9,
// amended): the candidates for the input — the known command names
// with the typed prefix while the name is being typed, and, after a
// complete name and a space, the command's Sub() hints with the
// argument prefix — and the accept prefix: what a candidate is
// prepended to on accept. Nothing for plain prompts and the //
// escape.
func (t *tui) completionLocked() (cands []menuCand, accept string, ok bool) {
	text := t.ed.text()
	if t.commands == nil || !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "//") {
		return nil, "", false
	}
	rest := text[1:]
	if sp := strings.IndexAny(rest, " \t"); sp >= 0 {
		name := rest[:sp]
		cmd, found := t.commands[name]
		if !found {
			return nil, "", false
		}
		subber, has := cmd.(command.Subber)
		if !has {
			return nil, "", false
		}
		for _, s := range subber.Sub() {
			if strings.HasPrefix(s.Name, rest[sp+1:]) {
				cands = append(cands, menuCand{name: s.Name, desc: s.Desc})
			}
		}
		return cands, "/" + name + " ", true
	}
	// a complete name with verbs (decision 9, amended): the verb menu
	// opens as soon as the name is whole — the operator sees the verbs
	// before typing the space, and an accept lands "/name verb ".
	if cmd, whole := t.commands[rest]; whole {
		if subber, has := cmd.(command.Subber); has {
			for _, sub := range subber.Sub() {
				cands = append(cands, menuCand{name: sub.Name, desc: sub.Desc})
			}
			return cands, "/" + rest + " ", true
		}
	}
	for _, name := range t.known {
		if strings.HasPrefix(name, rest) {
			cands = append(cands, menuCand{name: name, desc: t.commands[name].Description()})
		}
	}
	return cands, "/", true
}

// menuLinesLocked is the completion menu's rows (under mu; decision 2
// the cap, decision 9 the render): the visible candidate window — at
// most six rows, following the selection — and the dim "… N more"
// tail when the candidates run past six. Nil when the menu is closed.
func (t *tui) menuLinesLocked() []string {
	if !t.menuOpenLocked() {
		return nil
	}
	const cap = 6
	n := len(t.menuCands)
	start := t.menuSel - (cap - 1)
	if start < 0 {
		start = 0
	}
	if max := n - cap; start > max {
		start = max
	}
	if start < 0 {
		start = 0
	}
	end := start + cap
	if end > n {
		end = n
	}
	var rows []string
	for i := start; i < end; i++ {
		c := t.menuCands[i]
		row := t.theme.Paint(SlotAccent, c.name)
		if c.desc != "" {
			// a menu row is one terminal row (decision 10: the live
			// region is measured): the description takes what the
			// width leaves after the name, dotted when it overflows.
			room := t.width - displayWidth(c.name) - 3
			desc := c.desc
			if room <= 1 {
				desc = ""
			} else if displayWidth(desc) > room {
				desc = truncateWidth(t.theme, desc, room)
			}
			if desc != "" {
				row += t.theme.Paint(SlotText, "  "+desc)
			}
		}
		if i == t.menuSel {
			row = t.theme.Invert(row)
		}
		rows = append(rows, row)
	}
	if n > cap {
		rows = append(rows, t.theme.Paint(SlotDim, "… "+strconv.Itoa(n-cap)+" more"))
	}
	return rows
}

// tabTextLocked is the Tab key (under mu; decision 9, amended): while
// the menu is open, the selection steps down (the text stands); a
// single candidate is completed with its trailing space; otherwise a
// no-op. It reports the text to set and whether it changed.
func (t *tui) tabTextLocked() (string, bool) {
	if t.menuOpenLocked() {
		n := len(t.menuCands)
		t.menuSel = (t.menuSel + 1) % n
		return "", false
	}
	if len(t.menuCands) == 1 {
		_, accept, ok := t.completionLocked()
		if ok {
			next := accept + t.menuCands[0].name + " "
			if next != t.ed.text() {
				return next, true
			}
		}
	}
	return "", false
}

// menuAcceptLocked is the text the menu's selection replaces the
// input with (under mu; decision 9, amended): the candidate plus a
// trailing space, over the typed prefix (the name phase) or the
// command name and space (the argument phase).
func (t *tui) menuAcceptLocked() string {
	_, accept, _ := t.completionLocked()
	return accept + t.menuCands[t.menuSel].name + " "
}

// hintLocked is the inline ghost (under mu; decision 9, amended): the
// single candidate's remainder while it is being typed — a name or a
// sub — and, after a known name and a space, the command's
// description when it has no Sub() hints. Two or more candidates show
// the menu instead (menuLinesLocked); nothing for plain prompts and
// the // escape.
func (t *tui) hintLocked() string {
	text := t.inputText
	if t.commands == nil || !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "//") {
		return ""
	}
	rest := text[1:]
	if sp := strings.IndexAny(rest, " \t"); sp >= 0 {
		name := rest[:sp]
		cmd, ok := t.commands[name]
		if !ok {
			return ""
		}
		if subber, ok := cmd.(command.Subber); ok {
			// a single sub: the ghost; two or more: the menu
			// (menuLinesLocked) — never both.
			var match string
			for _, s := range subber.Sub() {
				if strings.HasPrefix(s.Name, rest[sp+1:]) {
					if match != "" {
						return ""
					}
					match = s.Name
				}
			}
			if match != "" {
				return strings.TrimPrefix(match, rest[sp+1:])
			}
			return ""
		}
		return cmd.Description()
	}
	var matches []string
	for _, name := range t.known {
		if strings.HasPrefix(name, rest) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 1 {
		return strings.TrimPrefix(matches[0], rest)
	}
	return ""
}

// inputLineLocked is the painted input row (under mu).
func (t *tui) inputLineLocked() string {
	line, _ := t.inputLineAndColLocked()
	return line
}

// maxInputRows is the input's visible height: the logical line wraps
// across up to five terminal rows (the terminal wraps, live.edit's
// row math places the cursor); a longer text scrolls a five-row
// window that follows the cursor. The Enter always commits the full
// text (the terminal wraps committed prose, decision 8).
const maxInputRows = 5

// inputLineAndColLocked is the painted input row (under mu) and the
// edit column over it. The painted line carries one trailing space —
// the cursor's cell, so the end-of-text cursor has a column (and, on
// a row-exact line, a row) inside the region's row count.
func (t *tui) inputLineAndColLocked() (string, int) {
	t.syncSizeLocked()
	glyph := t.theme.Glyph(GlyphPrompt)
	prefixCols := displayWidth(glyph) + 1
	text := displayInput(t.inputText)
	runes := []rune(text)
	width := t.width
	if width < 2 {
		width = 2
	}
	cursorCol := prefixCols + runeWidthSum(string(runes[:t.editPos])) + 1
	// the cursor's cell: a row-exact line leaves the end-of-text cursor
	// one column past the region's rows; the pad gives it a cell (and a
	// row) of its own. Any other line has the headroom already.
	lineCols := prefixCols + displayWidth(text)
	pad := ""
	if lineCols%width == 0 {
		pad = " "
	}
	totalCols := lineCols + len(pad)
	totalRows := (totalCols + width - 1) / width
	if totalRows <= maxInputRows {
		t.inputScroll = 0
		line := t.theme.Paint(SlotEmber, glyph) +
			t.theme.Paint(SlotText, " "+text+pad)
		// the inline hint (decision 9): ghost text after the typed text,
		// dim, display only. Single-row inputs only, cursor at the end
		// (mid-line edits keep the row clean); truncated to what the
		// width leaves.
		if totalRows == 1 && t.editPos == len(runes) {
			if hint := t.hintLocked(); hint != "" {
				room := width - lineCols - 2
				if room > 1 {
					if displayWidth(hint) > room {
						hint = truncateWidth(t.theme, hint, room)
					}
					line += t.theme.Paint(SlotDim, hint)
				}
			}
		}
		return line, cursorCol
	}
	// the window: maxInputRows rows over the wrapped line, scrolled so
	// the cursor's row stays visible. The slice is cut at the full
	// layout's row boundaries, so the windowed line wraps to the same
	// columns and live.edit's linear math holds shifted.
	cursorRow := (cursorCol - 1) / width
	scroll := t.inputScroll
	if max := totalRows - maxInputRows; scroll > max {
		scroll = max
	}
	if scroll < 0 {
		scroll = 0
	}
	if cursorRow < scroll {
		scroll = cursorRow
	}
	if cursorRow > scroll+maxInputRows-1 {
		scroll = cursorRow - maxInputRows + 1
	}
	t.inputScroll = scroll
	logical := glyph + " " + text + pad
	win := sliceCols(logical, scroll*width, (scroll+maxInputRows)*width)
	var line string
	if scroll == 0 {
		line = t.theme.Paint(SlotEmber, glyph) +
			t.theme.Paint(SlotText, strings.TrimPrefix(win, glyph))
	} else {
		line = t.theme.Paint(SlotText, win)
	}
	return line, cursorCol - scroll*width
}

// displayInput maps a pasted control rune to its one-cell input-row
// marker: the buffer keeps the real rune (the Enter commits it), the
// row shows the mark. The rune count is unchanged, so the edit
// position indexes both alike.
func displayInput(s string) string {
	if !strings.ContainsAny(s, "\n\t") {
		return s
	}
	r := []rune(s)
	for i, c := range r {
		switch c {
		case '\n':
			r[i] = '⏎'
		case '\t':
			r[i] = ' '
		}
	}
	return string(r)
}

// sliceCols is the display-column window [start, end) of s: the runes
// whose cells fall inside it. A wide rune straddling the start renders
// as a space (the column alignment holds); one straddling the end is
// dropped (the row runs a cell short).
func sliceCols(s string, start, end int) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		w := runeWidth(r)
		if col+w <= start {
			col += w
			continue
		}
		if col >= end {
			break
		}
		if col < start {
			b.WriteString(" ")
			col += w
			continue
		}
		if col+w > end {
			break
		}
		b.WriteRune(r)
		col += w
	}
	return b.String()
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
// slotAfterTool is lastSlot's sentinel after a tool block: the next
// streamed text of any slot gets the boundary blank (the spacing rule).
const slotAfterTool = "\x00after-tool"

func (t *tui) flow(slot, text string) {
	t.mu.Lock()
	// the reasoning boundary (decision 2's spacing rule): reasoning
	// closes and something else begins — close the pending line if it
	// is open, and put one blank row between (the collapse keeps it to
	// exactly one, whatever the model's own newlines did).
	boundary := (slot != "" && slot != SlotReasoning && t.lastSlot == SlotReasoning) ||
		(slot != "" && t.lastSlot == slotAfterTool)
	if boundary {
		sep := "\n"
		if len(t.pend) > 0 {
			sep = "\n\n"
		}
		t.pend = append(t.pend, seg{slot: slot, text: sep})
	}
	if slot != "" {
		t.lastSlot = slot
	}
	t.pend = append(t.pend, seg{slot: slot, text: text})
	lines := t.takeClosedLinesLocked()
	chunk := ""
	if len(lines) > 0 {
		chunk = strings.Join(lines, "\n") + "\n"
	}
	t.live.draw(chunk, t.liveLinesLocked(), t.statusLineLocked())
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
				lines = append(lines, t.commitLineLocked(cur)...)
				cur = nil
			} else if p != "" {
				cur = append(cur, seg{slot: s.slot, text: p})
			}
		}
	}
	t.pend = cur
	return lines
}

// commitLineLocked renders one closed line into its committed rows
// (under mu): the markdown pass for the model's text (the fence toggle
// and code mode; reasoning and mixed lines stay raw), then the word
// wrap for prose (decision 2, amended) — preformatted lines (code
// mode) commit whole, dim, indented two.
func (t *tui) commitLineLocked(cur []seg) []string {
	if t.codeMode {
		// inside a fence: the closing fence ends the mode (the line
		// drops); anything else commits preformatted.
		plain := paintFreeSegs(cur)
		if strings.HasPrefix(strings.TrimSpace(plain), "```") {
			t.codeMode = false
			t.codeLang = ""
			return nil
		}
		// the highlight (11, amended): the fence's language paints the
		// line by the lexical pass; unknown paints dim.
		return []string{"  " + highlightLine(t.theme, t.codeLang, plain)}
	}
	if t.markdown && isTextLine(cur) {
		out, fence, info := mdLine(t.theme, cur)
		if fence {
			t.codeMode = true
			t.codeLang = langOf(info)
			if info != "" {
				return []string{t.theme.Paint(SlotDim, "  "+info)}
			}
			return nil
		}
		cur = out
	}
	return wrapSegs(t.theme, t.width, cur)
}

// isTextLine: every segment is the model's text (or unpainted): the
// markdown pass's subject. Reasoning stays raw.
func isTextLine(segs []seg) bool {
	for _, s := range segs {
		if s.slot != SlotText && s.slot != "" {
			return false
		}
	}
	return true
}

// paintFreeSegs is the segments' text, unpainted.
func paintFreeSegs(segs []seg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.text)
	}
	return b.String()
}

// --- the dynamic loops ------------------------------------------------

// tickLoop advances the spinner's frame and rewrites the activity row
// in place (the setActivity op) — while a turn is live (the activity
// row is the bookkeeping's first by construction, the rest of the
// region — the menu, the input, the status row — standing).
func (t *tui) tickLoop() {
	for {
		select {
		case <-t.closed:
			return
		case <-t.ticks:
			t.mu.Lock()
			if (t.turnLive || t.compacting) && len(t.live.lines) > 0 {
				t.frame++
				t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
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
				if w, h, err := term.GetSize(t.fdi); err == nil && w > 0 {
					t.width = w
					t.height = h
					t.live.setWidth(w)
				}
			}
			if t.pg != nil {
				t.pg.width, t.pg.height = t.width, t.height
				t.pg.clamp()
				t.pg.render(t.live.w, t.theme)
			} else if len(t.live.lines) > 0 {
				t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
			}
			t.mu.Unlock()
		}
	}
}
