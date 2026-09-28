package openai

import (
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/imagemarker"
)

func wireMessages(msgs []core.Message) []wireMessage {
	return wireMessagesStyled(msgs, nil, wireStyle{reasoning: "reasoning_content"})
}

func wireMessagesWith(msgs []core.Message, imgs *imageStore) []wireMessage {
	return wireMessagesStyled(msgs, imgs, wireStyle{reasoning: "reasoning_content"})
}

func wireMessagesStyled(msgs []core.Message, imgs *imageStore, style wireStyle) []wireMessage {
	out := make([]wireMessage, 0, len(msgs))
	var pending []orderedMessage
	var owner callTable
	flush := func() {
		if len(pending) == 0 {
			return
		}
		sortOrdered(pending)
		for _, p := range pending {
			out = append(out, p.msg)
		}
		pending = nil
	}
	for _, m := range msgs {
		if m.Role == core.RoleAssistant {
			flush()
			out = append(out, encodeMessage(m, style))
			owner = tableOf(m)
			continue
		}
		if m.Role != core.RoleTool {
			flush()
			out = append(out, encodeMessage(m, style))
			owner = callTable{}
			continue
		}
		out = append(out, encodeMessage(m, style))
		if imgs == nil || owner.nameOf(m.ToolID) != viewToolName {
			continue
		}
		ref, ok := imagemarker.Parse(m.Content)
		if !ok {
			continue
		}
		pending = append(pending, orderedMessage{at: owner.indexOf(m.ToolID), msg: imageMessage(imgs, ref)})
	}
	flush()
	return out
}

func wireTools(specs []core.ToolSpec) []wireTool {
	out := make([]wireTool, 0, len(specs))
	for _, s := range specs {
		out = append(out, wireTool{
			Type:     "function",
			Function: wireToolFn{Name: s.Name, Description: s.Description, Parameters: s.Schema},
		})
	}
	return out
}
