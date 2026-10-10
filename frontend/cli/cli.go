package cli

import (
	"bufio"
	"context"
	"errors"
	"github.com/mrsirg97-rgb/rig/v2/command"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"io"
	"sort"
	"strings"
	"sync"
)

type cli struct {
	in  *bufio.Reader
	out io.Writer

	lines chan string
	slot  chan string
	wake  chan struct{}

	mu      sync.Mutex
	reading bool
	cancel  context.CancelFunc

	inbox []core.WorkerDone

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
		wake:  make(chan struct{}, 1),
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

func inputText(line string) string {
	if strings.HasPrefix(line, "//") {
		return command.Unescape(line)
	}
	return line
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
		if block, ok := c.drainInbox(); ok {
			c.mu.Lock()
			c.steeredLive = false
			c.turnCtx = ctx
			c.mu.Unlock()
			return block, nil
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
			return inputText(line), nil
		default:
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-c.wake:
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
			c.mu.Lock()
			c.turnCtx = ctx
			c.steeredLive = false
			c.mu.Unlock()
			return inputText(line), nil
		}
	}
}
