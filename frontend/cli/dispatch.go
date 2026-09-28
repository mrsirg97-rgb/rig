package cli

import (
	"context"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/command"
	"strings"
)

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
