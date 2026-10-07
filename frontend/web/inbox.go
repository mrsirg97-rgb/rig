package web

import (
	"github.com/mrsirg97-rgb/rig/v2/core"
)

// drainInbox hands over every worker that returned since the last turn, in the
// order they arrived. The feed already showed each return as it landed; this is
// the turn they start together.
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
