// Package cli is the stdin/stdout Frontend. Input is a blocking pull of one
// user message; Notify is a fire-and-forget observation of the turn stream,
// rendered as greppable plain text: deltas verbatim, the execution bracket
// as status lines, faults as marked lines, and one usage line at the
// explicit turn boundary (TurnEnd).
//
// Steering (SPEC_HARDENING decision 4): a background line reader feeds
// either the blocked Input (between turns) or the slot (during a live
// turn, where it also interrupts the turn via the handle the loop handed
// the last Input ctx). One slot, latest wins: not a mailbox. Sequential
// delivery means no locking beyond the channels.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

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

	// per-turn usage totals, summed across the turn's model calls and
	// printed at TurnEnd.
	prompt     int
	completion int
	cacheRead  int

	current string // the tool in flight, from the last ToolStart
}

// New wires the frontend over an input reader and output writer.
func New(in io.Reader, out io.Writer) core.Frontend {
	c := &cli{
		in:    bufio.NewReader(in),
		out:   out,
		lines: make(chan string, 1),
		slot:  make(chan string, 1),
	}
	go c.readLoop()
	return c
}

// readLoop owns stdin. A line that lands while Input is blocked delivers
// direct (between turns); a line that lands while a turn is live goes to
// the slot and interrupts the turn (the interrupt is what makes it land at
// the re-entry instead of after the turn's own work).
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
		c.mu.Lock()
		direct := c.reading
		c.mu.Unlock()
		if direct {
			c.lines <- strings.TrimRight(line, "\r\n")
		} else {
			c.steer(strings.TrimRight(line, "\r\n"))
		}
	}
}

// steer queues the line (single slot, latest wins) and interrupts the live
// turn. A dead handle is a no-op: between turns the loop already cancelled
// the turn's context, and the queued line simply waits for the next Input.
func (c *cli) steer(line string) {
	select {
	case c.slot <- line:
	default:
		select {
		case <-c.slot: // the previous intent: replaced, latest wins
		default:
		}
		c.slot <- line
	}
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Input blocks for one user message. The steering slot is delivered before
// blocking (the contract). Blank lines are no-ops; EOF ends the REPL; a
// cancelled context surfaces its error.
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
			if strings.TrimSpace(line) != "" {
				return line, nil
			}
			continue // a blank steering line: no-op, keep pulling
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
			return line, nil
		}
	}
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
