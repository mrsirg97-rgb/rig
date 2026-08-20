package tui

import (
	"fmt"
	"strings"
)

type StatusIn struct {
	Model     string
	Effort    string
	Window    int
	Role      string
	Approve   string
	Session   string
	Up        int
	Down      int
	CacheRead int
}

func RenderStatus(t Theme, in StatusIn) string {
	b := renderTitle(t)
	if in.Session != "" {

		id := in.Session
		if len(id) > 12 {
			id = id[:12]
		}
		b += t.Paint(SlotDim, "session "+id) + "\n"
	}
	return b
}

func renderTitle(t Theme) string {
	b := t.Paint(SlotDim, "welcome to") + "\n"
	if t.Glyph(GlyphPrompt) == ">" {
		return b + t.Paint(SlotEmber, "rig") + "\n"
	}
	for _, row := range titleRows {
		b += t.Paint(SlotEmber, row) + "\n"
	}
	return b
}

var titleRows = []string{
	"█▀▄ █ █▀▀",
	"█▀▄ █ █ █",
	"▀ ▀ ▀ ▀▀▀",
}

func RenderStatusLine(t Theme, model, effort, role, approveMode string, used, window int, hasUsed bool, up, down, cacheRead int) string {
	if model == "" {
		return ""
	}
	sep := t.Paint(SlotDim, " · ")
	row1 := t.Paint(SlotText, model)
	if hasUsed && window > 0 {
		pct := used * 100 / window
		slot := SlotDim
		switch {
		case pct >= 90:
			slot = SlotError
		case pct >= 70:
			slot = SlotWarn
		}
		row1 += sep + t.Paint(slot, formatTokens(used)+"/"+formatTokens(window))
	}
	var row2 string
	if effort != "" {
		row2 = t.Paint(effortSlot(t, effort), effort) + sep
	}
	row2 += t.Paint(SlotDim, abbrevRole(role)) + sep
	if approveMode == "manual" {
		row2 += t.Paint(SlotWarn, "manual")
	} else {
		row2 += t.Paint(SlotDim, "auto")
	}
	hit := 0
	if up > 0 {
		hit = cacheRead * 100 / up
	}
	row3 := t.Paint(SlotDim, fmt.Sprintf("up %s down %s · cache r %s %d%%",
		formatTokens(up), formatTokens(down), formatTokens(cacheRead), hit))
	return row1 + "\n" + row2 + "\n" + row3
}

func effortSlot(t Theme, level string) string {
	if len(level) == 0 {
		return SlotAccent
	}
	key := "effort" + strings.ToUpper(level[:1]) + strings.ToLower(level[1:])
	if t.Slot(key) != "" {
		return key
	}
	return SlotAccent
}

func abbrevRole(role string) string {
	switch role {
	case "architect":
		return "arch"
	case "reviewer":
		return "rev"
	case "", "default":
		return "default"
	}
	return role
}
