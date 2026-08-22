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

type tui struct {
	theme Theme
	in    io.Reader
	fdi   int

	mu            sync.Mutex
	width         int
	height        int
	pg            *pager
	fromPager     bool
	live          *live
	inputText     string
	editPos       int
	inputScroll   int
	phase         string
	frame         int
	showReasoning bool
	turnLive      bool
	compacting    bool

	turnEstablished bool
	reading         bool
	turnCtx         context.Context
	cancel          context.CancelFunc
	slot            string
	hasSlot         bool
	steeredLive     bool
	started         bool
	quit            bool
	rawOld          *term.State

	prompt, completion, cacheRead int

	pend []seg

	toolName string
	toolArgs []byte

	markdown bool
	codeMode bool

	pendCol int

	lastSlot string

	statusIn func(context.Context) StatusIn
	news     func(context.Context) string
	commands map[string]core.Command
	known    []string
	env      any

	menuCands     []menuCand
	menuSel       int
	menuDead      bool
	menuNavigated bool

	statusModel   string
	statusEffort  string
	statusRole    string
	statusApprove string
	statusWindow  int
	statusUsed    int
	statusHasUsed bool

	statusUp, statusDown, statusCache int

	askText  string
	askReply chan bool

	ed editor
	kp keyParser

	pending    chan string
	wake       chan struct{}
	readerOnce sync.Once
	closed     chan struct{}
	closeOnce  sync.Once
	ticker     *time.Ticker

	ticks <-chan time.Time
	winch <-chan struct{}

	sizeOf func() (int, int, bool)
}

func WithSize(f func() (int, int, bool)) Option {
	return func(t *tui) { t.sizeOf = f }
}

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

type Option func(*tui)

func WithTheme(t Theme) Option { return func(tu *tui) { tu.theme = t } }

func WithWidth(w int) Option { return func(t *tui) { t.width = w } }

func WithStatus(f func(context.Context) StatusIn) Option {
	return func(t *tui) { t.statusIn = f }
}

func WithNews(f func(context.Context) string) Option {
	return func(t *tui) { t.news = f }
}

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
			command.EffortHints(cmds, e)
		}
	}
}

func WithTicks(ch <-chan time.Time) Option { return func(t *tui) { t.ticks = ch } }

func WithWinch(ch <-chan struct{}) Option { return func(t *tui) { t.winch = ch } }

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
	t.live.onSuspended = t.repaintPagerLocked
	if t.rawOld != nil {

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

func defaultTheme() Theme {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		panic("tui: " + err.Error())
	}
	return th
}

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

func IsTerminal(fd uintptr) bool { return term.IsTerminal(int(fd)) }

func (t *tui) Close() {
	t.closeOnce.Do(func() { close(t.closed) })
	if t.ticker != nil {
		t.ticker.Stop()
	}
	t.mu.Lock()
	if t.pg != nil {

		t.pg = nil
		io.WriteString(t.live.w, altOff)
	}
	t.mu.Unlock()
	if t.rawOld != nil {
		io.WriteString(t.live.w, pasteOff)
		term.Restore(t.fdi, t.rawOld)
	}
}

func (t *tui) readLoop() {

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

const escDelay = 30 * time.Millisecond

func (t *tui) onKey(k key, r rune) {

	t.mu.Lock()
	asking := t.askReply != nil
	cancel := t.cancel
	t.mu.Unlock()
	if asking {
		switch {
		case r == 'y' || r == 'Y':
			t.askAnswer(true)
		case r == 'n' || r == 'N':
			t.askAnswer(false)
		case k == keyEsc:
			t.askAnswer(false)
			if cancel != nil {
				cancel()
			}
		case k == keyCtrlC:
			t.askAnswer(false)
			t.quitSession()
		}
		return
	}
	if !t.fromPager && t.pagerKey(k, r) {
		return
	}
	switch k {
	case keyPgUp:
		t.enterPager()
	case keyEnter:
		t.onEnter()
	case keyCtrlC:

		t.quitSession()
	case keyCtrlD:

		if strings.TrimSpace(t.ed.text()) == "" {
			t.quitSession()
		}
	case keyCtrlT:

		t.mu.Lock()
		t.showReasoning = !t.showReasoning
		t.mu.Unlock()
	case keyEsc:

		if t.closeMenu() {
			return
		}
		t.mu.Lock()
		live := t.turnLive
		cancel := t.cancel
		t.mu.Unlock()
		if strings.TrimSpace(t.ed.text()) == "" && live {
			if cancel != nil {
				cancel()
			}
			return
		}
		t.ed.apply(keyEsc, 0)
		t.mu.Lock()
		t.menuSyncLocked()
		t.mu.Unlock()
		t.paintInput()
	case keyTab:

		t.mu.Lock()
		if t.menuOpenLocked() {
			t.menuNavigated = true
		}
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

		t.mu.Lock()
		if t.menuOpenLocked() {
			t.menuNavigated = true
			n := len(t.menuCands)
			t.menuSel = (t.menuSel - 1 + n) % n
		}
		t.mu.Unlock()
		t.paintInput()
	case keyUp, keyDown:

		t.mu.Lock()
		open := t.menuOpenLocked()
		if open {
			t.menuNavigated = true
			n := len(t.menuCands)
			if k == keyDown {
				t.menuSel = (t.menuSel + 1) % n
			} else {
				t.menuSel = (t.menuSel - 1 + n) % n
			}
		}
		t.mu.Unlock()
		if open {
			t.paintInput()
			return
		}
		t.ed.apply(k, r)
		t.mu.Lock()
		t.menuSyncLocked()
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

func (t *tui) menuSyncLocked() {
	t.menuCands, _, _ = t.completionLocked()
	t.menuSel = 0
	t.menuDead = false
	t.menuNavigated = false
}

func (t *tui) menuOpenLocked() bool {
	return len(t.menuCands) >= 2 && !t.menuDead
}

func (t *tui) closeMenu() bool {
	t.mu.Lock()
	open := t.menuOpenLocked()
	if open {
		t.menuDead = true
		t.menuNavigated = false
	}
	t.mu.Unlock()
	if open {
		t.paintInput()
	}
	return open
}

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

		t.exitPagerLocked()
		t.mu.Unlock()
		t.quitSession()
		return true
	default:

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

func (t *tui) enterPager() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pg != nil {
		return
	}
	t.pg = newPager(t.live.hist, t.width, t.height)
	t.pg.footer = t.footerLocked()
	t.live.suspend()
	io.WriteString(t.live.w, altOn+t.pg.frame(t.theme))
}

func (t *tui) footerLocked() []string {
	rows := t.liveLinesLocked()
	return append(rows, statusRows(t.statusLineLocked())...)
}

func (t *tui) repaintPagerLocked() {
	if t.pg == nil {
		return
	}
	t.pg.lines = t.live.hist
	t.pg.footer = t.footerLocked()
	t.pg.clamp()
	t.pg.render(t.live.w, t.theme)
}

func (t *tui) exitPagerLocked() {
	if t.pg == nil {
		return
	}
	t.pg = nil
	io.WriteString(t.live.w, altOff)
	t.live.resume()
}

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

func (t *tui) onEnter() {
	t.mu.Lock()

	if t.menuOpenLocked() && t.menuNavigated {
		accept := t.menuAcceptLocked()
		t.mu.Unlock()
		t.ed.setText(accept)
		t.mu.Lock()
		t.menuSyncLocked()
		t.mu.Unlock()
		t.paintInput()
		return
	}

	if !t.menuOpenLocked() {
		if next, ok := t.tabTextLocked(); ok {
			t.mu.Unlock()
			t.ed.setText(next)
			t.mu.Lock()
		}
	}
	t.mu.Unlock()
	line, submitted := t.ed.apply(keyEnter, 0)
	if !submitted || strings.TrimSpace(line) == "" {
		return
	}
	t.mu.Lock()
	t.syncSizeLocked()

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

		t.turnLive = true
		t.phase = "thinking"
		t.frame = 0
	}

	t.live.enter(full, "", t.inputLineLocked(), t.statusLineLocked())
	t.mu.Unlock()
	if isCmd {
		t.pending <- line
		return
	}
	if wasLive {

		t.mu.Lock()
		t.pend = nil
		t.mu.Unlock()
		t.steer(line)
		return
	}
	_ = established

	t.pending <- line
}

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

func (t *tui) onInputEOF() {
	if line, submitted := t.ed.apply(keyEnter, 0); submitted && strings.TrimSpace(line) != "" {
		t.pending <- line
	}
	t.quitSession()
	close(t.pending)
}

func (t *tui) Input(ctx context.Context) (string, error) {
	t.mu.Lock()
	if cancel, ok := core.InterruptFrom(ctx); ok {
		t.cancel = cancel
	}
	t.reading = true

	if !t.started {

		t.started = true
		committed := t.sessionStartLocked()
		t.live.draw(committed, t.liveLinesLocked(), t.statusLineLocked())
	}
	t.mu.Unlock()

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
				continue
			}

			t.startTurnLocked(ctx)
			return line, nil
		}

		select {
		case line, ok := <-t.pending:
			if !ok {
				return "", io.EOF
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

func (t *tui) consume(ctx context.Context, line string) (string, bool) {
	if strings.TrimSpace(line) == "" {
		return "", false
	}
	if t.commands != nil && command.IsCommandLine(line) {
		t.dispatch(ctx, line)
		return "", false
	}
	out := line
	if strings.HasPrefix(line, "//") {
		out = command.Unescape(line)
	}

	t.startTurnLocked(ctx)
	return out, true
}

func (t *tui) startTurnLocked(ctx context.Context) {
	t.mu.Lock()
	t.turnCtx = ctx
	t.steeredLive = false
	t.turnLive = true
	t.turnEstablished = false
	t.pend = nil
	t.phase = "thinking"
	t.frame = 0
	t.toolName = ""
	t.toolArgs = nil
	t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
	t.mu.Unlock()
}

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

func (t *tui) Notify(ev core.Event) {
	switch ev.(type) {
	case core.ReasoningDelta, core.TextDelta, core.ToolStart, core.ToolResult,
		core.Done, core.Fault, core.Compacted, core.TurnEnd:

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
		t.phase = e.Call.Name
		t.mu.Unlock()

		t.flow(SlotText, "\n")
	case core.ToolResult:
		t.mu.Lock()
		block := RenderToolBlock(t.theme, t.toolName, t.toolArgs, e.Content, e.Err != nil, e.Duration)
		t.phase = "thinking"
		t.toolName = ""
		t.toolArgs = nil
		t.lastSlot = slotAfterTool
		t.mu.Unlock()
		t.commit(block)
	case core.Done:

		t.mu.Lock()
		t.prompt += e.Usage.Prompt
		t.completion += e.Usage.Completion
		t.cacheRead += e.Usage.CacheRead
		if e.Usage.Prompt > 0 || e.Usage.Completion > 0 {
			t.statusUsed = e.Usage.Prompt + e.Usage.Completion
			t.statusHasUsed = true
		}

		t.statusUp, t.statusDown, t.statusCache = t.prompt, t.completion, t.cacheRead
		t.mu.Unlock()
		t.flow("", "\n")
	case core.Fault:

		t.mu.Lock()
		t.compacting = false
		fault := RenderFault(t.theme, e.Err)
		t.mu.Unlock()
		t.flow("", "\n")
		t.commit(fault)
	case core.Compacting:

		t.mu.Lock()
		t.phase = "compacting"
		t.frame = 0
		if !t.turnLive {
			t.compacting = true
		}

		if len(t.live.lines) > 0 {
			t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
		}
		t.mu.Unlock()
	case core.Compacted:

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

		t.mu.Lock()
		t.lastSlot = ""
		t.pendCol = 0
		t.codeMode = false
		pending := len(t.pend) > 0
		t.mu.Unlock()
		if pending {
			t.flow("", "\n")
		}
		t.mu.Lock()

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

	}
}

func (t *tui) commit(chunk string) {
	t.mu.Lock()
	t.live.draw(chunk, t.liveLinesLocked(), t.statusLineLocked())
	t.mu.Unlock()
}

func (t *tui) liveLinesLocked() []string {
	t.syncSizeLocked()

	var lines []string
	if t.turnLive || t.compacting {

		if pl := paintSegs(t.theme, t.pend); pl != "" {
			lines = append(lines, pl, "")
		} else if !t.live.lastBlank {

			lines = append(lines, "")
		}
		lines = append(lines, t.activityLineLocked())
	}

	if len(lines) > 0 || !t.live.lastBlank {
		lines = append(lines, "")
	}
	if t.askReply != nil {

		lines = append(lines, t.askLineLocked())
	} else if ml := t.menuLinesLocked(); len(ml) > 0 {
		lines = append(lines, ml...)
	}
	return append(lines, t.inputLineLocked())
}

func (t *tui) askLineLocked() string {
	return t.theme.Paint(SlotWarn, "approve "+t.askText+"?") +
		t.theme.Paint(SlotDim, "  [y run · n decline · esc interrupts]")
}

func (t *tui) statusLineLocked() string {
	st := RenderStatusLine(t.theme, t.statusModel, t.statusEffort, t.statusRole, t.statusApprove, t.statusUsed, t.statusWindow, t.statusHasUsed,
		t.statusUp, t.statusDown, t.statusCache)
	if st == "" {
		return ""
	}

	return "\n" + st
}

func (t *tui) sessionStartLocked() string {
	var b strings.Builder
	if t.statusIn != nil {
		in := t.statusIn(context.Background())
		t.statusModel = in.Model
		t.statusEffort = in.Effort
		t.statusRole = in.Role
		t.statusApprove = in.Approve
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

func (t *tui) dispatch(ctx context.Context, line string) {

	name, args := command.Parse(line)
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
		refresh = true
	case name == "role" && args != "":
		refresh = true
	}
	if out != "" {
		t.live.draw(t.theme.Paint(SlotText, out), t.liveLinesLocked(), t.statusLineLocked())
	}
	if refresh && t.statusIn != nil {

		in := t.statusIn(context.Background())
		t.statusModel = in.Model
		t.statusEffort = in.Effort
		t.statusRole = in.Role
		t.statusApprove = in.Approve
		t.statusWindow = in.Window
		if fresh {
			t.statusUsed = 0
			t.statusHasUsed = false
		}
		t.statusUp, t.statusDown, t.statusCache = in.Up, in.Down, in.CacheRead
		t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
	}
}

func (t *tui) commandOpeningLocked(name, args string) string {
	s := t.theme.Paint(SlotAccent, "/"+name)
	if args != "" {
		s += t.theme.Paint(SlotDim, " · ") + t.theme.Paint(SlotText, args)
	}
	return s
}

func (t *tui) Ask(ctx context.Context, prompt string) bool {
	reply := make(chan bool, 1)
	t.mu.Lock()
	t.askText, t.askReply = prompt, reply
	t.mu.Unlock()
	t.paintInput()
	select {
	case ans := <-reply:
		return ans
	case <-ctx.Done():
		t.mu.Lock()
		t.askText, t.askReply = "", nil
		t.mu.Unlock()
		t.paintInput()
		return false
	}
}

func (t *tui) askAnswer(ans bool) {
	t.mu.Lock()
	reply := t.askReply
	t.askText, t.askReply = "", nil
	t.mu.Unlock()
	if reply == nil {
		return
	}
	reply <- ans
	t.paintInput()
}

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

func (t *tui) ClearSlot() {
	t.mu.Lock()
	t.slot = ""
	t.hasSlot = false
	t.mu.Unlock()
}

func (t *tui) LiveTurn() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.turnLive
}

func (t *tui) activityLineLocked() string {
	frame := "|/-\\"[t.frame%4]
	label := t.phase
	if label == "" {
		label = "thinking"
	}

	return t.theme.Paint(SlotEmber, string(frame)+" "+label)
}

type menuCand struct {
	name string
	desc string
}

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

	rows = append(rows, t.theme.Paint(SlotDim, "tab/↓ pick · enter runs"))
	return rows
}

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

func (t *tui) menuAcceptLocked() string {
	_, accept, _ := t.completionLocked()
	return accept + t.menuCands[t.menuSel].name + " "
}

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

func (t *tui) inputLineLocked() string {
	line, _ := t.inputLineAndColLocked()
	return line
}

const maxInputRows = 5

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

func paintLines(t Theme, slot, text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = t.Paint(slot, l)
	}
	return strings.Join(lines, "\n")
}

type seg struct {
	slot string
	text string
}

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

func (t *tui) expandTabsLocked(text string) string {
	if !strings.ContainsAny(text, "\t\n") && t.pendCol >= 0 {
		for _, r := range text {
			t.pendCol += runeWidth(r)
		}
		return text
	}
	var b strings.Builder
	for _, r := range text {
		switch r {
		case '\n':
			t.pendCol = 0
			b.WriteRune(r)
		case '\t':
			n := 8 - t.pendCol%8
			b.WriteString(strings.Repeat(" ", n))
			t.pendCol += n
		default:
			t.pendCol += runeWidth(r)
			b.WriteRune(r)
		}
	}
	return b.String()
}

const slotAfterTool = "\x00after-tool"

func (t *tui) flow(slot, text string) {
	t.mu.Lock()

	boundary := (slot != "" && slot != SlotReasoning && t.lastSlot == SlotReasoning) ||
		(slot != "" && t.lastSlot == slotAfterTool)
	if boundary {
		sep := "\n"
		if len(t.pend) > 0 {
			sep = "\n\n"
		}
		t.pend = append(t.pend, seg{slot: slot, text: sep})
		t.pendCol = 0
	}
	if slot != "" {
		t.lastSlot = slot
	}
	t.pend = append(t.pend, seg{slot: slot, text: t.expandTabsLocked(text)})
	lines := t.takeClosedLinesLocked()
	chunk := ""
	if len(lines) > 0 {
		chunk = strings.Join(lines, "\n") + "\n"
	}
	t.live.draw(chunk, t.liveLinesLocked(), t.statusLineLocked())
	t.mu.Unlock()
}

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

func (t *tui) commitLineLocked(cur []seg) []string {
	if t.codeMode {

		plain := paintFreeSegs(cur)
		if strings.HasPrefix(strings.TrimSpace(plain), "```") {
			t.codeMode = false
			return nil
		}
		return []string{t.theme.Paint(SlotDim, "  "+plain)}
	}
	if t.markdown && isTextLine(cur) {
		out, fence, info := mdLine(t.theme, cur)
		if fence {
			t.codeMode = true
			if info != "" {
				return []string{t.theme.Paint(SlotDim, "  "+info)}
			}
			return nil
		}
		cur = out
	}
	return wrapSegs(t.theme, t.width, cur)
}

func isReasoningLine(segs []seg) bool {
	for _, s := range segs {
		if s.slot != SlotReasoning && s.slot != "" {
			return false
		}
	}
	return true
}

func isTextLine(segs []seg) bool {
	for _, s := range segs {
		if s.slot != SlotText && s.slot != "" {
			return false
		}
	}
	return true
}

func paintFreeSegs(segs []seg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.text)
	}
	return b.String()
}

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
