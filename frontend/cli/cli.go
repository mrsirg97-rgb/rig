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
	in  *bufio.Reader
	out io.Writer

	lines chan string
	slot  chan string

	mu      sync.Mutex
	reading bool
	cancel  context.CancelFunc

	turnCtx context.Context

	steeredLive bool

	commands map[string]core.Command
	known    []string
	env      any

	prompt     int
	completion int
	cacheRead  int

	current string
}

type Option func(*cli)

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
			e.Steer = c
		}
	}
}

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

func (c *cli) readLoop() {
	for {
		line, err := c.in.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && line != "" {
				c.lines <- strings.TrimRight(line, "\r\n")
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

			c.lines <- s
			continue
		}
		c.mu.Lock()
		c.steeredLive = true
		c.mu.Unlock()
		c.steer(s)
	}
}

func (c *cli) steer(line string) {
	c.queueSlot(line)
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *cli) queueSlot(line string) {
	select {
	case c.slot <- line:
	default:
		select {
		case <-c.slot:
		default:
		}
		c.slot <- line
	}
}

func (c *cli) Input(ctx context.Context) (string, error) {
	c.mu.Lock()
	if cancel, ok := core.InterruptFrom(ctx); ok {
		c.cancel = cancel
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
				continue
			}
			if c.commands != nil && command.IsCommandLine(line) {
				c.dispatch(ctx, line)
				continue
			}

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
				continue
			}
			if c.commands != nil && command.IsCommandLine(line) {
				c.dispatch(ctx, line)
				continue
			}
			out := line
			if strings.HasPrefix(line, "//") {
				out = command.Unescape(line)
			}

			c.mu.Lock()
			c.turnCtx = ctx
			c.steeredLive = false
			c.mu.Unlock()
			return out, nil
		}
	}
}

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

		fmt.Fprint(c.out, strings.TrimRight(out, "\n")+"\n")
	}
}

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

func (c *cli) ClearSlot() {
	select {
	case <-c.slot:
	default:
	}
}

func (c *cli) LiveTurn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnCtx != nil && c.turnCtx.Err() == nil
}

func (c *cli) Notify(ev core.Event) {
	switch e := ev.(type) {
	case core.TextDelta:
		io.WriteString(c.out, e.Text)
	case core.ReasoningDelta:

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

		c.prompt += e.Usage.Prompt
		c.completion += e.Usage.Completion
		c.cacheRead += e.Usage.CacheRead
	case core.Compacting:

		io.WriteString(c.out, "\u29c9 compacting\u2026\n")
	case core.Compacted:

		fmt.Fprintf(c.out, "⧉ compact: -%s kept %s · summary ↑%s ↓%s\n",
			formatTokens(e.Dropped), formatTokens(e.Kept),
			formatTokens(e.Usage.Prompt), formatTokens(e.Usage.Completion))
	case core.Fault:
		fmt.Fprintf(c.out, "\n[fault] %v\n", e.Err)
	case core.TurnEnd:

		hit := 0
		if c.prompt > 0 {
			hit = int(int64(c.cacheRead) * 100 / int64(c.prompt))
		}
		fmt.Fprintf(c.out, "↑%s ↓%s · cache %s %d%%\n",
			formatTokens(c.prompt), formatTokens(c.completion), formatTokens(c.cacheRead), hit)
		c.prompt, c.completion, c.cacheRead = 0, 0, 0
	}
}

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
