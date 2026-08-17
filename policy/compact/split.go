package compact

import (
	"github.com/mrsirg97-rgb/rig/core"
)

// split is the keep-recent cut (decision 3): the largest suffix whose
// calibrated estimate is within the budget — never empty (at least the
// last message: the budget is a ceiling, not a floor) — and the tail's
// first message is never a RoleTool result whose call is in the
// transcript: a result whose call sits in the older prefix would dangle
// on the wire, so the boundary slides back to the batch's assistant
// message. A multi-call batch is atomic, and the tail may exceed the
// budget by that batch's estimate — bounded by one batch. A dangling
// result (the call is not in the transcript, the resume shape) may start
// the tail: there is no pair to keep whole.
//
// The tail's budget is the calibrated estimate of the tail's own
// messages — the anchor is not used here: it counts the history the tail
// does not include, and a tail sized by it would shrink with the
// transcript's age (decision 3).
func split(msgs []core.Message, factor float64, keepRecent int) (older, tail []core.Message) {
	if len(msgs) == 0 {
		return nil, nil
	}
	budget := func(m core.Message) float64 { return float64(Estimate([]core.Message{m})) * factor }
	i := len(msgs) - 1
	sum := budget(msgs[i])
	for i > 0 {
		if sum+budget(msgs[i-1]) > float64(keepRecent) {
			break
		}
		sum += budget(msgs[i-1])
		i--
	}
	for msgs[i].Role == core.RoleTool {
		j := callIn(msgs, msgs[i].ToolID)
		if j < 0 {
			break // a dangling result: the tail may start here
		}
		i = j // slide back to the batch's assistant message
	}
	if i == 0 {
		return nil, msgs
	}
	return msgs[:i], msgs[i:]
}

// callIn finds the assistant message that carries the call with id, or -1.
func callIn(msgs []core.Message, id string) int {
	if id == "" {
		return -1
	}
	for k, m := range msgs {
		if m.Role != core.RoleAssistant {
			continue
		}
		for _, c := range m.ToolCalls {
			if c.ID == id {
				return k
			}
		}
	}
	return -1
}
