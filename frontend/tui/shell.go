package tui

import (
	"bufio"
	"context"
	"github.com/mrsirg97-rgb/rig/v2/command"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"golang.org/x/term"
	"io"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"
)

type tui struct {
	theme Theme
	in    io.Reader
	fdi   int
	tty   bool

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
	dirty         bool
	flowChunks    []string
	showReasoning bool
	turnLive      bool
	compacting    bool
	notices       []core.Notice
	noticing      bool
	noticeFrame   int
	aside         string
	asideAt       time.Time
	phaseLines    []string
	phaseOpen     string
	phaseHidden   int

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
	cost                          float64

	pend    []seg
	pw      pendWrap
	pendGen int

	toolName   string
	toolArgs   []byte
	toolStarts map[string]startInfo

	markdown bool
	codeMode bool

	pendCol int

	lastSlot string

	statusIn     func(context.Context) StatusIn
	titleName    string
	titleRows    []string
	titleTagline string
	commands     map[string]core.Command
	known        []string
	env          any

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
	statusCost                        float64
	statusRows                        []string

	swarm core.SwarmStatus

	bandKind   string
	bandSpawns map[int]time.Time

	inbox []core.WorkerDone

	idleInterrupt func()

	askText  string
	askReply chan bool

	ed editor
	kp keyParser

	pending      chan string
	wake         chan struct{}
	readerOnce   sync.Once
	closed       chan struct{}
	closeOnce    sync.Once
	ticker       *time.Ticker
	tickStop     chan struct{}
	statusTicker *time.Ticker

	ticks       <-chan time.Time
	statusTicks <-chan time.Time
	winch       <-chan struct{}
	stopWinch   func()

	sizeOf func() (int, int, bool)
}

type startInfo struct {
	name string
	args []byte
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
		t.live.setHeight(h)
	}
}

type Option func(*tui)

func WithWidth(w int) Option { return func(t *tui) { t.width = w } }

func WithStatus(f func(context.Context) StatusIn) Option {
	return func(t *tui) { t.statusIn = f }
}

func WithStatusTick(d time.Duration) Option {
	return func(t *tui) {
		if t.statusTicker != nil {
			t.statusTicker.Stop()
			t.statusTicker = nil
			t.statusTicks = nil
		}
		if d > 0 {
			t.statusTicker = time.NewTicker(d)
			t.statusTicks = t.statusTicker.C
		}
	}
}

// WithIdleInterrupt wires the interrupt gesture for the moment there is no turn
// to interrupt: an esc with an empty line stops the running workers. A session
// without a delegate passes nothing, and the gesture keeps clearing the line.
func WithIdleInterrupt(stop func()) Option {
	return func(t *tui) { t.idleInterrupt = stop }
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
			e.Lines = t.lines
			command.ModelHints(cmds, e)
			command.EffortHints(cmds, e)
		}
	}
}

func WithTitle(name string, rows []string, tagline string) Option {
	return func(t *tui) {
		t.titleName = name
		t.titleRows = rows
		t.titleTagline = tagline
	}
}

func WithTicks(ch <-chan time.Time) Option { return func(t *tui) { t.ticks = ch } }

func WithWinch(ch <-chan struct{}) Option { return func(t *tui) { t.winch = ch } }

func New(in io.Reader, out io.Writer, theme Theme, opts ...Option) core.Frontend {
	t := &tui{
		theme:         theme,
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
		t.tty = true
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
	if t.winch == nil && t.tty {
		t.winch, t.stopWinch = signalWinch()
	}
	if t.winch != nil {
		go t.winchLoop()
	}
	return t
}

func signalWinch() (<-chan struct{}, func()) {
	sig := make(chan os.Signal, 1)
	done := make(chan struct{})
	exited := make(chan struct{})
	signal.Notify(sig, syscall.SIGWINCH)
	ch := make(chan struct{}, 1)
	go func() {
		defer close(exited)
		for {
			select {
			case <-done:
				signal.Stop(sig)
				return
			case <-sig:
				select {
				case ch <- struct{}{}:
				default:
				}
			}
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(done)
			<-exited
		})
	}
	return ch, stop
}

func IsTerminal(fd uintptr) bool { return term.IsTerminal(int(fd)) }

func (t *tui) Close() {
	t.closeOnce.Do(func() { close(t.closed) })
	if t.ticker != nil {
		t.ticker.Stop()
	}
	if t.statusTicker != nil {
		t.statusTicker.Stop()
	}
	if t.stopWinch != nil {
		t.stopWinch()
		t.stopWinch = nil
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
