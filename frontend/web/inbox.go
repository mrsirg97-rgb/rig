package web

import (
	"github.com/mrsirg97-rgb/rig/v2/core"
)

func (c *chat) drainInbox() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.inbox) == 0 {
		return "", false
	}
	block := core.WorkerBlock(c.inbox)
	c.inbox = nil
	return block, true
}
