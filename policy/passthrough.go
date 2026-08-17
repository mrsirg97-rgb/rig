// Package policy carries prompt-assembly implementations. v1 ships the
// passthrough: system prompt plus transcript, verbatim.
package policy

import (
	"context"

	"github.com/mrsirg97-rgb/rig/core"
)

type passthrough struct{ system string }

// Passthrough assembles the system prompt (when set) followed by the
// transcript, verbatim. Pure across repeated assemblies.
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
