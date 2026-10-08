package cli

import (
	"fmt"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

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
