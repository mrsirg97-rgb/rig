// Package cli is the stdin/stdout Frontend. Input is a blocking pull of
// one user message; Notify is a fire-and-forget observation of the turn
// stream, rendered as greppable plain text: deltas verbatim, the
// execution bracket as status lines, faults as marked lines, and one
// usage line at the explicit turn boundary (TurnEnd).
//
// Steering (SPEC_HARDENING decision 4): a background line reader feeds
// either the blocked Input (between turns) or the slot (during a live
// turn, where it also interrupts the turn via the handle the loop handed
// the last Input ctx). One slot, latest wins: not a mailbox. Sequential
// delivery means no locking beyond the channels.
//
// Commands (SPEC_COMMANDS decision 1): a line whose first byte is '/'
// and whose second is not '/' is a command line, full stop — it is
// dispatched inside Input, before it returns to the loop, exactly as a
// blank line is consumed today; the loop's contract "Input returns one
// message" is a prompt, and the loop never sees a command. '//' is the
// escape (one slash consumed); an unknown command is a loud line naming
// the known set, never silently a prompt.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
)

type cli struct {
	in  *bufio.Reader // owned by the reader goroutine
	out io.Writer

	lines chan string // reader output, buffered 1: the between-turns path
	slot  chan string // steering slot, buffered 1: latest wins

	mu      sync.Mutex
	reading bool               // Input is in flight: lines deliver direct, not to the slot
	cancel  context.CancelFunc // the interrupt handle from the last Input ctx

	// turnCtx is the ctx of the last delivered Input — the live turn's,
	// if any: the loop only calls a new Input after the previous turn has
	// ended (its ctx cancelled), so a live turn is exactly "the last
	// delivered ctx is alive". LiveTurn (the compact / new / sessions
	// resume refusal) reads this; in the CLI it is structurally false at
	// dispatch (the loop is in awaiting_input).
	turnCtx context.Context
	// steeredLive is the "an interrupt just landed" fact: the last line
	// was queued by the reader while a turn was live. The steer command
	// reports it ('· turn interrupted'); a delivered line consumes it.
	steeredLive bool

	// the command dispatch (SPEC_COMMANDS 1): the registry, the sorted
	// known names for the refusal voice, and the root's env, carried to
	// each Run. Empty = no dispatch (the compat rule, both sides).
	commands map[string]core.Command
	known    []string
	env      any

	// per-turn usage totals, summed across the turn's model calls and
	// printed at TurnEnd.
	prompt     int
	completion int
	cacheRead  int

	current string // the tool in flight, from the last ToolStart
}

// Option configures the frontend.
type Option func(*cli)

// WithCommands registers the user commands (SPEC_COMMANDS 1): the
// prefix dispatch inside Input, before a line becomes a prompt — the
// loop never sees a command. env is the root's (decision 2); the
// frontend-owned seam (Steer) is filled here, with this frontend as the
// Steerer — the slot, the interrupt handle, and the liveness fact are
// this frontend's contract (7's), not the root's.
func WithCommands(cmds []core.Command, env any) Option {
	return func(c *cli) {
		c.commands = make(map[string]core.Command, len(cmds))
		c.known = make([]string, 0, len(cmds))
		for _, cmd := range cmds {
			c.commands[cmd.Name()] = cmd
			c.known = append(c.known, cmd.Name())
		}
		sort.Strings(c.known)
		c.env = env
		if e, ok := env.(*command.Env); ok {
			e.Steer = c // the dispatcher fills the frontend-owned seam
		}
	}
}

// New wires the frontend over an input reader and output writer.
func New(in io.Reader, out io.Writer, opts ...Option) core.Frontend {
	c := &cli{
		in:    bufio.NewReader(in),
		out:   out,
		lines: make(chan string, 1),
		slot:  make(chan string, 1),
	}
	for _, opt := range opts {
		opt(c)
	}
	go c.readLoop()
	return c
}

// readLoop owns stdin. A line that lands while Input is blocked delivers
// direct (between turns); a line that lands while a turn is live goes to
// the slot and interrupts the turn (the interrupt is what makes it land
// at the re-entry instead of after the turn's own work), and marks the
// "an interrupt just landed" fact the steer command reports.
func (c *cli) readLoop() {
	for {
		line, err := c.in.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && line != "" {
				c.lines <- strings.TrimRight(line, "\r\n") // final line without a newline
			}
			close(c.lines)
			return
		}
		s := strings.TrimRight(line, "\r\n")
		c.mu.Lock()
		direct := c.reading
		live := c.turnCtx != nil && c.turnCtx.Err() == nil
		c.mu.Unlock()
		if direct {
			c.lines <- s
			continue
		}
		if !live {
			// no turn is live and Input is not parked (startup, a paste
			// burst, the window between turns): the line is ordinary input,
			// delivered in order on the next Input. The slot's latest-wins
			// is steering semantics and would silently drop all but the
			// last line here.
			c.lines <- s
			continue
		}
		c.mu.Lock()
		c.steeredLive = true
		c.mu.Unlock()
		c.steer(s)
	}
}

// steer queues the line (single slot, latest wins) and interrupts the
// live turn. A dead handle is a no-op: between turns the loop already
// cancelled the turn's context, and the queued line simply waits for the
// next Input.
func (c *cli) steer(line string) {
	c.queueSlot(line)
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// queueSlot is the slot's latest-wins write.
func (c *cli) queueSlot(line string) {
	select {
	case c.slot <- line:
	default:
		select {
		case <-c.slot: // the previous intent: replaced, latest wins
		default:
		}
		c.slot <- line
	}
}

// Input blocks for one user message. The steering slot is delivered
// before blocking (the contract). A command line is dispatched and
// consumed there, before any line becomes a prompt (SPEC_COMMANDS 1):
// the dispatch prints exactly one line — the error if the command
// refused, else the reply if non-empty — and Input keeps pulling, so a
// command's effect (a queued steer) is delivered on the next pass. Blank
// lines are no-ops; EOF ends the REPL; a cancelled context surfaces its
// error.
func (c *cli) Input(ctx context.Context) (string, error) {
	c.mu.Lock()
	if cancel, ok := core.InterruptFrom(ctx); ok {
		c.cancel = cancel // the loop's handle for this turn
	}
	c.reading = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.reading = false
		c.mu.Unlock()
	}()
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		select {
		case line := <-c.slot:
			if strings.TrimSpace(line) == "" {
				continue // a blank steering line: no-op, keep pulling
			}
			if c.commands != nil && command.IsCommandLine(line) {
				c.dispatch(ctx, line)
				continue
			}
			// a delivered slot line consumed the "interrupt landed" fact
			// (it is the line that broke the turn and became a prompt).
			c.mu.Lock()
			c.steeredLive = false
			c.turnCtx = ctx
			c.mu.Unlock()
			return line, nil
		default:
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case line, ok := <-c.lines:
			if !ok {
				return "", io.EOF
			}
			if strings.TrimSpace(line) == "" {
				continue // blank line: no-op
			}
			if c.commands != nil && command.IsCommandLine(line) {
				c.dispatch(ctx, line)
				continue
			}
			out := line
			if strings.HasPrefix(line, "//") {
				out = command.Unescape(line) // the escape consumes one slash
			}
			// this Input's ctx is the turn's: live until the loop cancels
			// it — the LiveTurn fact the commands read. A line typed at a
			// quiet prompt is a clean boundary: the "interrupt landed" fact
			// (a line queued during a live turn) does not survive it.
			c.mu.Lock()
			c.turnCtx = ctx
			c.steeredLive = false
			c.mu.Unlock()
			return out, nil
		}
	}
}

// dispatch consumes one command line (SPEC_COMMANDS 1): the prefix rule
// names it, the registry runs it, and exactly one line is printed —
// the error if the command refused, else the reply if non-empty — one
// line, newline terminated, on the frontend's stdout (10). The line
// never reaches the loop.
func (c *cli) dispatch(ctx context.Context, line string) {
	name, args := command.Parse(line)
	cmd, ok := c.commands[name]
	if !ok {
		fmt.Fprintf(c.out, "unknown command: %s (known: %s)\n", name, strings.Join(c.known, ", "))
		return
	}
	out, err := cmd.Run(ctx, args, c.env)
	if err != nil {
		fmt.Fprintln(c.out, err)
		return
	}
	if out != "" {
		// the dispatcher owns the line's frame: the command's output is
		// the bytes, and exactly one newline ends the line — a
		// multi-line listing (the plugins' rows, a sessions show)
		// carries its own trailing one, and the frame is not a second.
		fmt.Fprint(c.out, strings.TrimRight(out, "\n")+"\n")
	}
}

// --- the Steerer (SPEC_COMMANDS 2): the frontend-owned seam ---------

// Steer queues the text (latest wins, replacing whatever is there) and
// reports whether an interrupt landed: the line was queued during a
// live turn (the reader's fact), or a turn is live right now and was
// interrupted (a TUI dispatching from a keypress mid-turn — structural
// for the CLI at the prompt, where no turn is live at dispatch).
func (c *cli) Steer(text string) bool {
	c.queueSlot(text)
	c.mu.Lock()
	live := c.turnCtx != nil && c.turnCtx.Err() == nil
	wasLive := c.steeredLive
	c.steeredLive = false
	cancel := c.cancel
	c.mu.Unlock()
	if live && cancel != nil {
		cancel()
		return true
	}
	return wasLive
}

// Interrupt interrupts a live turn only — no text is queued — and
// reports the same way Steer does.
func (c *cli) Interrupt() bool {
	c.mu.Lock()
	live := c.turnCtx != nil && c.turnCtx.Err() == nil
	wasLive := c.steeredLive
	c.steeredLive = false
	cancel := c.cancel
	c.mu.Unlock()
	if live && cancel != nil {
		cancel()
		return true
	}
	return wasLive
}

// ClearSlot drops the queued intent: a new session does not inherit it
// (SPEC_COMMANDS 4).
func (c *cli) ClearSlot() {
	select {
	case <-c.slot:
	default:
	}
}

// LiveTurn: a turn is live right now (compact / new / sessions resume
// refuse on it). In the CLI it is structurally false at dispatch (the
// loop is in awaiting_input); a TUI dispatching from a keypress
// mid-turn sees true.
func (c *cli) LiveTurn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnCtx != nil && c.turnCtx.Err() == nil
}

// Notify observes one stream event and renders it to the output. Events
// the CLI does not name are ignored (the compat rule): unknown is noise,
// never a misread.
func (c *cli) Notify(ev core.Event) {
	switch e := ev.(type) {
	case core.TextDelta:
		io.WriteString(c.out, e.Text)
	case core.ReasoningDelta:
		// the thinking is visible, verbatim; the TUI styles it (10)
		io.WriteString(c.out, e.Text)
	case core.ToolStart:
		c.current = e.Call.Name
		fmt.Fprintf(c.out, "\n● %s\n", e.Call.Name)
	case core.ToolResult:
		outcome := "✓"
		if e.Err != nil {
			outcome = "✕"
		}
		fmt.Fprintf(c.out, "%s %s %s\n", c.current, outcome, e.Duration)
	case core.Done:
		io.WriteString(c.out, "\n")
		// the turn totals accumulate across the turn's model calls
		c.prompt += e.Usage.Prompt
		c.completion += e.Usage.Completion
		c.cacheRead += e.Usage.CacheRead
	case core.Compacting:
		// the loader's line (SPEC_COMPACT 5, amended): the summary call
		// at deep context can prefill for minutes; say so once.
		io.WriteString(c.out, "\u29c9 compacting\u2026\n")
	case core.Compacted:
		// SPEC_COMPACT 5: compaction is a transcript event — one line, the
		// numbers as pane's formatTokens shapes; the TUI styles it (10).
		fmt.Fprintf(c.out, "⧉ compact: -%s kept %s · summary ↑%s ↓%s\n",
			formatTokens(e.Dropped), formatTokens(e.Kept),
			formatTokens(e.Usage.Prompt), formatTokens(e.Usage.Completion))
	case core.Fault:
		fmt.Fprintf(c.out, "\n[fault] %v\n", e.Err)
	case core.TurnEnd:
		// the explicit turn boundary: one usage line. hit = CacheRead /
		// Prompt: the OpenAI-style wire reports cached tokens as a subset
		// of prompt.
		hit := 0
		if c.prompt > 0 {
			hit = c.cacheRead * 100 / c.prompt
		}
		fmt.Fprintf(c.out, "↑%s ↓%s · cache %s %d%%\n",
			formatTokens(c.prompt), formatTokens(c.completion), formatTokens(c.cacheRead), hit)
		c.prompt, c.completion, c.cacheRead = 0, 0, 0
	}
}

// formatTokens: raw under 1000, one-decimal k under 10k, rounded k under
// 1M, else M.
func formatTokens(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1000000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
}
