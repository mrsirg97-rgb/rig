package cli

import (
	"fmt"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

// drainInbox hands over every worker that returned since the last turn and
// prints each one's head line, so the terminal shows what the model was just
// told. The inbox is never latest-wins: what arrived first is read first.
func (c *cli) drainInbox() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.inbox) == 0 {
		return "", false
	}
	block := core.WorkerBlock(c.inbox)
	for _, d := range c.inbox {
		fmt.Fprintln(c.out, d.Head())
	}
	c.inbox = nil
	return block, true
}
