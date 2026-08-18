// Package tui is the terminal frontend over the frozen runtime
// (SPEC_TUI): the same events, the same commands, the same tools,
// rendered in pane's design language with fewer parts. The terminal
// owns scrollback (decision 1); the TUI commits blocks on event
// boundaries and redraws a live region capped at decision 2's rows
// (the menu's, the input's, the status).
//
// The split: theme.go, status.go, commit.go, and tools_render.go are
// pure renderers (state in, bytes out); live.go turns them into the
// escape stream; input.go is the key parser and line editor; tui.go is
// the Frontend shell (the reader goroutine, the command dispatch, the
// completion menu, the status line's refresh points).
package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The palette slots (decision 7's fixed vocabulary).
const (
	SlotText      = "text"
	SlotDim       = "dim"
	SlotAccent    = "accent"
	SlotSuccess   = "success"
	SlotError     = "error"
	SlotWarn      = "warn"
	SlotRule      = "rule"
	SlotReasoning = "reasoning"
)

// slotVocabulary is the sorted slot list the refusals name (7).
var slotVocabulary = []string{SlotAccent, SlotDim, SlotError, SlotReasoning, SlotRule, SlotSuccess, SlotText, SlotWarn}

// The glyph slots, in both sets.
const (
	GlyphPending = "pending" // ○ / [ ]
	GlyphActive  = "active"  // ◐ / [~]
	GlyphDone    = "done"    // ● / [*]
	GlyphFail    = "fail"    // ✕ / [x]
	GlyphOK      = "ok"      // ✓ / v (the close line's success mark, 4)
	GlyphCompact = "compact" // ⧉ / =
	GlyphPrompt  = "prompt"  // ❯ / >
	GlyphBarOn   = "bar"     // ▰ / #
	GlyphBarOff  = "baroff"  // ▱ / -
	GlyphDot     = "dot"     // · / .
)

var (
	glyphsUnicode = map[string]string{
		GlyphPending: "○", GlyphActive: "◐", GlyphDone: "●", GlyphFail: "✕", GlyphOK: "✓",
		GlyphCompact: "⧉", GlyphPrompt: "❯", GlyphBarOn: "▰", GlyphBarOff: "▱", GlyphDot: "·",
	}
	glyphsASCII = map[string]string{
		GlyphPending: "[ ]", GlyphActive: "[~]", GlyphDone: "[*]", GlyphFail: "[x]", GlyphOK: "v",
		GlyphCompact: "=", GlyphPrompt: ">", GlyphBarOn: "#", GlyphBarOff: "-", GlyphDot: ".",
	}
)

// Theme is a resolved palette: named slots over hex colors, one glyph
// set, and the output mode (truecolor or the 256 downconvert, decision
// 7: named, automatic, not configurable).
type Theme struct {
	name      string // the base theme's shipped name
	TrueColor bool
	slots     map[string]string
	glyphs    map[string]string
}

// Name is the base theme's shipped name.
func (t Theme) Name() string { return t.name }

// Slot is one palette slot's hex value (#rrggbb).
func (t Theme) Slot(name string) string { return t.slots[name] }

// Glyph is one glyph set entry.
func (t Theme) Glyph(name string) string { return t.glyphs[name] }

// SGR and Paint (ansi.go) wrap text in the slot's color and a reset,
// truecolor when the terminal reports it, else the nearest 256 index.

// Shipped is the palette table: oled (default), paper, and the two
// phosphor ramps (decision 7). The phosphor palettes are four
// brightnesses of one hue: text on the brightest, accent and success on
// the next tier, error, warn, and reasoning on the middle, dim and rule
// on the deepest, so the state hierarchy survives without hue.
var Shipped = map[string]map[string]string{
	"oled": {
		SlotText:      "#d8d8d8",
		SlotDim:       "#6e6e6e",
		SlotAccent:    "#61afef",
		SlotSuccess:   "#98c379",
		SlotError:     "#e06c75",
		SlotWarn:      "#e5c07b",
		SlotRule:      "#3c3c3c",
		SlotReasoning: "#9a86c8",
	},
	"paper": {
		SlotText:      "#24292f",
		SlotDim:       "#6e7781",
		SlotAccent:    "#0969da",
		SlotSuccess:   "#1a7f37",
		SlotError:     "#cf222e",
		SlotWarn:      "#9a6700",
		SlotRule:      "#d0d7de",
		SlotReasoning: "#6f42c1",
	},
	"p1": {
		SlotText:      "#aaffc2",
		SlotAccent:    "#57e07c",
		SlotSuccess:   "#57e07c",
		SlotWarn:      "#2aa050",
		SlotError:     "#2aa050",
		SlotReasoning: "#2aa050",
		SlotDim:       "#156b33",
		SlotRule:      "#156b33",
	},
	"p3": {
		SlotText:      "#ffd28a",
		SlotAccent:    "#e0a245",
		SlotSuccess:   "#e0a245",
		SlotWarn:      "#b07820",
		SlotError:     "#b07820",
		SlotReasoning: "#b07820",
		SlotDim:       "#7a5215",
		SlotRule:      "#7a5215",
	},
}

// shippedNames is the sorted shipped set the refusals name (7).
var shippedNames = func() []string {
	out := make([]string, 0, len(Shipped))
	for n := range Shipped {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}()

// ParseHex is the #rrggbb parse (7): exactly a hash plus six hex digits,
// case-insensitive, normalized to lowercase.
func ParseHex(s string) (r, g, b int, err error) {
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0, fmt.Errorf("%s: expected #rrggbb", s)
	}
	// ParseUint: bitSize 8 is signed for ParseInt (max 127), which would
	// refuse every high byte of a truecolor color.
	for i, p := range []int{1, 3, 5} {
		n, e := strconv.ParseUint(s[p:p+2], 16, 8)
		if e != nil {
			return 0, 0, 0, fmt.Errorf("%s: expected #rrggbb", s)
		}
		dst := &r
		if i == 1 {
			dst = &g
		}
		if i == 2 {
			dst = &b
		}
		*dst = int(n)
	}
	return r, g, b, nil
}

// Nearest256 is the downconvert (7): the nearest xterm-256 entry over
// the cube (16-231) and the grayscale ramp (232-255). Exact matches win;
// the named pairs are pinned by the tests.
func Nearest256(hex string) int {
	r, g, b, err := ParseHex(hex)
	if err != nil {
		return 16 // the refusal happens upstream; 16 is the floor
	}
	best, bestD := 16, 1<<30
	for n := 16; n < 256; n++ {
		pr, pg, pb := palette256(n)
		d := (pr-r)*(pr-r) + (pg-g)*(pg-g) + (pb-b)*(pb-b)
		if d < bestD {
			bestD, best = d, n
		}
	}
	return best
}

// palette256 is the xterm cube (16-231) and grayscale ramp (232-255).
func palette256(n int) (r, g, b int) {
	if n >= 232 {
		v := 8 + 10*(n-232)
		return v, v, v
	}
	n -= 16
	lvl := []int{0, 95, 135, 175, 215, 255}
	return lvl[n/36], lvl[(n/6)%6], lvl[n%6]
}

// ResolveTheme is the selection and override (decision 7): the shipped
// name from settings.theme (absent = oled), with theme.json's base,
// slot overrides, and glyph set over it; the file wins when both are
// set (the more specific intent, named). Refusals are loud and name the
// known vocabulary, in SPEC_CONFIG's voice (the root adds the path).
func ResolveTheme(settingsTheme string, doc json.RawMessage, trueColor bool) (Theme, error) {
	if settingsTheme != "" {
		if _, ok := Shipped[settingsTheme]; !ok {
			return Theme{}, fmt.Errorf("theme: unknown value %q (known: %s)", settingsTheme, strings.Join(shippedNames, ", "))
		}
	}
	base := "oled"
	if settingsTheme != "" {
		base = settingsTheme
	}
	slots := map[string]string{}
	for k, v := range Shipped[base] {
		slots[k] = v
	}
	glyphs := map[string]string{}
	for k, v := range glyphsUnicode {
		glyphs[k] = v
	}
	if len(doc) == 0 {
		return Theme{name: base, TrueColor: trueColor, slots: slots, glyphs: glyphs}, nil
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(doc, &keys); err != nil || keys == nil {
		return Theme{}, errors.New("theme.json: expected a JSON object")
	}
	var unknown []string
	for k := range keys {
		if k != "base" && k != "glyphs" && k != "slots" {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return Theme{}, fmt.Errorf("theme.json: unknown key %q (known: base, glyphs, slots)", unknown[0])
	}
	var baseRaw json.RawMessage
	hasBase := false
	if raw, ok := keys["base"]; ok {
		hasBase = true
		baseRaw = raw
	}
	if !hasBase {
		return Theme{}, fmt.Errorf("theme.json: base required (known: %s)", strings.Join(shippedNames, ", "))
	}
	var baseName string
	if err := json.Unmarshal(baseRaw, &baseName); err != nil || baseName == "" {
		return Theme{}, fmt.Errorf("theme.json: base: expected a string, got %s", strings.TrimSpace(string(baseRaw)))
	}
	sh, ok := Shipped[baseName]
	if !ok {
		return Theme{}, fmt.Errorf("theme.json: unknown base %q (known: %s)", baseName, strings.Join(shippedNames, ", "))
	}
	slots = map[string]string{}
	for k, v := range sh {
		slots[k] = v
	}
	if raw, ok := keys["slots"]; ok {
		var smap map[string]json.RawMessage
		if err := json.Unmarshal(raw, &smap); err != nil || smap == nil {
			return Theme{}, errors.New("theme.json: slots: expected an object, got " + kindOf(raw))
		}
		var names []string
		for k := range smap {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			if !isSlot(k) {
				return Theme{}, fmt.Errorf("theme.json: unknown slot %q (known: %s)", k, strings.Join(slotVocabulary, ", "))
			}
			var v string
			if err := json.Unmarshal(smap[k], &v); err != nil {
				return Theme{}, fmt.Errorf("theme.json: slots: %s: expected a string, got %s", k, strings.TrimSpace(string(smap[k])))
			}
			if _, _, _, err := ParseHex(v); err != nil {
				return Theme{}, fmt.Errorf("theme.json: slots: %s: %v", k, err)
			}
			slots[k] = strings.ToLower(v)
		}
	}
	if raw, ok := keys["glyphs"]; ok {
		var gs string
		if err := json.Unmarshal(raw, &gs); err != nil {
			return Theme{}, fmt.Errorf("theme.json: glyphs: expected a string, got %s", strings.TrimSpace(string(raw)))
		}
		switch gs {
		case "unicode":
		case "ascii":
			for k, v := range glyphsASCII {
				glyphs[k] = v
			}
		default:
			return Theme{}, fmt.Errorf("theme.json: glyphs: unknown %q (known: ascii, unicode)", gs)
		}
	}
	return Theme{name: baseName, TrueColor: trueColor, slots: slots, glyphs: glyphs}, nil
}

func isSlot(k string) bool {
	for _, s := range slotVocabulary {
		if k == s {
			return true
		}
	}
	return false
}

// kindOf names a JSON value's kind for the refusal voice.
func kindOf(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(s, "["):
		return "array"
	case strings.HasPrefix(s, "{"):
		return "object"
	case s == "null" || s == "true" || s == "false":
		return s
	case strings.HasPrefix(s, "\""):
		return "string"
	default:
		return "number"
	}
}
