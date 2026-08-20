package policy

import (
	"context"

	"github.com/mrsirg97-rgb/rig/core"
)

type passthrough struct{ system string }

func Passthrough(system string) core.ContextPolicy {
	return &passthrough{system: system}
}

func (p *passthrough) Assemble(ctx context.Context, s *core.Session) ([]core.Message, error) {
	msgs := make([]core.Message, 0, len(s.Messages)+1)
	if p.system != "" {
		msgs = append(msgs, core.Message{Role: core.RoleSystem, Content: p.system})
	}
	return append(msgs, s.Messages...), nil
}
