package rem

import (
	"fmt"
	"strings"
)

func indent(text string) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n")
}

func renderMemoryLine(h Hit) string {
	head := fmt.Sprintf("m%d [%.2f %s] %s · %s", h.ID, h.EffectiveStrength, h.Match, h.ScopeLabel, h.Kind)
	if h.SupersededBy != nil {
		head += fmt.Sprintf(" · superseded by m%d", *h.SupersededBy)
	}
	return head + "\n" + indent(h.Content)
}

func renderHits(hits []Hit, where string) string {
	if len(hits) == 0 {
		return "(no memories " + where + ")"
	}
	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(renderMemoryLine(h))
	}
	return b.String()
}
