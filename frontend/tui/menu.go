package tui

import (
	"github.com/mrsirg97-rgb/rig/v2/command"
	"strconv"
	"strings"
)

type menuCand struct {
	name string
	desc string
}

func (t *tui) completionLocked() (cands []menuCand, accept string, ok bool) {
	text := t.ed.text()
	if t.commands == nil || !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "//") {
		return nil, "", false
	}
	rest := text[1:]
	if sp := strings.IndexAny(rest, " \t"); sp >= 0 {
		name := rest[:sp]
		cmd, found := t.commands[name]
		if !found {
			return nil, "", false
		}
		subber, has := cmd.(command.Subber)
		if !has {
			return nil, "", false
		}
		for _, s := range subber.Sub() {
			if strings.HasPrefix(s.Name, rest[sp+1:]) {
				cands = append(cands, menuCand{name: s.Name, desc: s.Desc})
			}
		}
		return cands, "/" + name + " ", true
	}

	if cmd, whole := t.commands[rest]; whole {
		if subber, has := cmd.(command.Subber); has {
			for _, sub := range subber.Sub() {
				cands = append(cands, menuCand{name: sub.Name, desc: sub.Desc})
			}
			return cands, "/" + rest + " ", true
		}
	}
	for _, name := range t.known {
		if strings.HasPrefix(name, rest) {
			cands = append(cands, menuCand{name: name, desc: t.commands[name].Description()})
		}
	}
	return cands, "/", true
}

func (t *tui) menuLinesLocked(maxRows int) []string {
	if !t.menuOpenLocked() {
		return nil
	}
	n := len(t.menuCands)
	if n < 1 {
		return nil
	}
	window := 6
	if n < window {
		window = n
	}
	showTail := n > window
	showHint := true
	for window+boolInt(showTail)+boolInt(showHint) > maxRows {
		switch {
		case window > 1:
			window--
			showTail = n > window
		case showHint:
			showHint = false
		case showTail:
			showTail = false
		default:
			return nil
		}
	}
	start := t.menuSel - (window - 1)
	if start < 0 {
		start = 0
	}
	if max := n - window; start > max {
		start = max
	}
	if start < 0 {
		start = 0
	}
	end := start + window
	if end > n {
		end = n
	}
	var rows []string
	for i := start; i < end; i++ {
		c := t.menuCands[i]
		row := t.theme.Paint(SlotAccent, c.name)
		if c.desc != "" {

			room := t.width - displayWidth(c.name) - 3
			desc := c.desc
			if room <= 1 {
				desc = ""
			} else if displayWidth(desc) > room {
				desc = truncateWidth(t.theme, desc, room)
			}
			if desc != "" {
				row += t.theme.Paint(SlotText, "  "+desc)
			}
		}
		if i == t.menuSel {
			row = t.theme.Invert(row)
		}
		rows = append(rows, row)
	}
	if showTail {
		rows = append(rows, t.theme.Paint(SlotDim, "… "+strconv.Itoa(n-end)+" more"))
	}
	if showHint {
		rows = append(rows, t.theme.Paint(SlotDim, "tab/↓ pick · enter runs"))
	}
	return rows
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (t *tui) tabTextLocked() (string, bool) {
	if t.menuOpenLocked() {
		n := len(t.menuCands)
		t.menuSel = (t.menuSel + 1) % n
		return "", false
	}
	if len(t.menuCands) == 1 {
		_, accept, ok := t.completionLocked()
		if ok {
			next := accept + t.menuCands[0].name + " "
			if next != t.ed.text() {
				return next, true
			}
		}
	}
	return "", false
}

func (t *tui) menuAcceptLocked() string {
	_, accept, _ := t.completionLocked()
	return accept + t.menuCands[t.menuSel].name + " "
}

func (t *tui) hintLocked() string {
	text := t.inputText
	if t.commands == nil || !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "//") {
		return ""
	}
	rest := text[1:]
	if sp := strings.IndexAny(rest, " \t"); sp >= 0 {
		name := rest[:sp]
		cmd, ok := t.commands[name]
		if !ok {
			return ""
		}
		if subber, ok := cmd.(command.Subber); ok {

			var match string
			for _, s := range subber.Sub() {
				if strings.HasPrefix(s.Name, rest[sp+1:]) {
					if match != "" {
						return ""
					}
					match = s.Name
				}
			}
			if match != "" {
				return strings.TrimPrefix(match, rest[sp+1:])
			}
			return ""
		}
		return cmd.Description()
	}
	var matches []string
	for _, name := range t.known {
		if strings.HasPrefix(name, rest) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 1 {
		return strings.TrimPrefix(matches[0], rest)
	}
	return ""
}
