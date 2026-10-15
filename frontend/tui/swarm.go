package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v3/core"
)

func IsDelegateBand(st core.SwarmStatus) bool {
	for _, w := range st.Workers {
		if w.Role == delegateRole {
			return true
		}
	}
	return false
}

const delegateRole = "delegate"

func wordFor(n int) string {
	if n == 1 {
		return "worker"
	}
	return "workers"
}

func swarmRule(t Theme) string {
	return t.Paint(SlotDim, strings.Repeat(t.Glyph(GlyphDot), 4))
}

func swarmAnyRunning(workers []core.SwarmWorker) bool {
	for _, w := range workers {
		if w.State == "running" {
			return true
		}
	}
	return false
}

func swarmAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
