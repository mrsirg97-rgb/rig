package compact

import (
	"github.com/mrsirg97-rgb/rig/core"
)

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
			break
		}
		i = j
	}
	if i == 0 {
		return nil, msgs
	}
	return msgs[:i], msgs[i:]
}

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
