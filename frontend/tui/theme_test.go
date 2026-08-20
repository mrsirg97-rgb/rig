package tui_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/frontend/tui"
)

// --- the shipped tables (decision 7) ---

func TestShippedThemesCarryEverySlot(t *testing.T) {
	want := []string{"oled", "paper", "p1", "p3"}
	for _, name := range want {
		th, err := tui.ResolveTheme(name, nil, true)
		if err != nil {
			t.Fatalf("ResolveTheme(%q): %v", name, err)
		}
		for _, slot := range []string{"text", "dim", "accent", "success", "error", "warn", "rule", "reasoning"} {
			v := th.Slot(slot)
			if !strings.HasPrefix(v, "#") || len(v) != 7 {
				t.Fatalf("%s slot %s = %q, want #rrggbb", name, slot, v)
			}
		}
	}
}

func TestOledIsTheDefault(t *testing.T) {
	th, err := tui.ResolveTheme("", nil, true)
	if err != nil {
		t.Fatalf("no settings, no theme.json: %v", err)
	}
	if th.Name() != "oled" {
		t.Fatalf("default = %q, want oled", th.Name())
	}
}

func TestGlyphSets(t *testing.T) {
	un, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for slot, want := range map[string]string{
		"pending": "○", "active": "◐", "done": "●", "fail": "✕", "ok": "✓",
		"compact": "⧉", "prompt": "❯", "bar": "▰", "baroff": "▱", "dot": "·",
	} {
		if got := un.Glyph(slot); got != want {
			t.Fatalf("unicode glyph %s = %q, want %q", slot, got, want)
		}
	}
	doc := []byte(`{"base":"oled","glyphs":"ascii"}`)
	as, err := tui.ResolveTheme("oled", json.RawMessage(doc), true)
	if err != nil {
		t.Fatal(err)
	}
	for slot, want := range map[string]string{
		"pending": "[ ]", "active": "[~]", "done": "[*]", "fail": "[x]", "ok": "v",
		"compact": "=", "prompt": ">", "bar": "#", "baroff": "-", "dot": ".",
	} {
		if got := as.Glyph(slot); got != want {
			t.Fatalf("ascii glyph %s = %q, want %q", slot, got, want)
		}
	}
}

// --- theme.json schema (decision 7's named cases) ---

func TestThemeJSONSchemaRefusals(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string // a fragment of the refusal voice
	}{
		{"unknown base", `{"base":"plasma"}`, `unknown base "plasma" (known: oled, p1, p3, paper)`},
		{"missing base", `{"glyphs":"ascii"}`, `base required (known: oled, p1, p3, paper)`},
		{"unknown slot", `{"base":"oled","slots":{"hue":"#ff9e64"}}`, `unknown slot "hue" (known: accent, dim, effortHigh, effortLow, effortMax, effortMedium, effortMinimal, effortOff, effortXhigh, ember, error, reasoning, rule, success, text, warn)`},
		{"bad hex short", `{"base":"oled","slots":{"accent":"#ff9e6"}}`, `accent: #ff9e6: expected #rrggbb`},
		{"bad hex digits", `{"base":"oled","slots":{"accent":"#ffzz64"}}`, `accent: #ffzz64: expected #rrggbb`},
		{"bad hex no hash", `{"base":"oled","slots":{"accent":"ff9e64"}}`, `accent: ff9e64: expected #rrggbb`},
		{"slot not a string", `{"base":"oled","slots":{"accent":19801700}}`, `accent: expected a string, got 19801700`},
		{"slots not an object", `{"base":"oled","slots":["accent"]}`, `slots: expected an object, got array`},
		{"bad glyphs", `{"base":"oled","glyphs":"emoji"}`, `glyphs: unknown "emoji" (known: ascii, unicode)`},
		{"unknown key", `{"base":"oled","palette":"oled"}`, `unknown key "palette" (known: base, glyphs, slots)`},
		{"not an object", `[1,2,3]`, `theme.json: expected a JSON object`},
		{"empty object missing base", `{}`, `base required (known: oled, p1, p3, paper)`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := tui.ResolveTheme("", json.RawMessage(c.doc), true)
			if err == nil {
				t.Fatalf("doc %s: no refusal", c.doc)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("refusal %q does not name %q", err.Error(), c.want)
			}
		})
	}
}

func TestThemeJSONSlotOverrideAndGlyphs(t *testing.T) {
	doc := []byte(`{"base":"paper","slots":{"accent":"#FF9E64","reasoning":"#5a5a5a"},"glyphs":"ascii"}`)
	th, err := tui.ResolveTheme("oled", json.RawMessage(doc), true)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := tui.ResolveTheme("paper", nil, true)
	if th.Slot("accent") != "#ff9e64" {
		t.Fatalf("accent = %q, want the file's #ff9e64 (lowercase-normalized)", th.Slot("accent"))
	}
	if th.Slot("reasoning") != "#5a5a5a" {
		t.Fatalf("reasoning = %q, want #5a5a5a", th.Slot("reasoning"))
	}
	if th.Slot("text") != base.Slot("text") {
		t.Fatalf("unlisted slot text = %q, want the base theme's", th.Slot("text"))
	}
	if th.Glyph("done") != "[*]" {
		t.Fatalf("glyphs = %q, want the ascii set", th.Glyph("done"))
	}
}

func TestThemeJSONWinsOverSettings(t *testing.T) {
	// the file is the more specific intent (decision 7, named): with both
	// set, the file's base wins.
	doc := []byte(`{"base":"p3"}`)
	th, err := tui.ResolveTheme("paper", json.RawMessage(doc), true)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := tui.ResolveTheme("p3", nil, true)
	if th.Slot("text") != want.Slot("text") {
		t.Fatalf("text = %q, want p3's %q (the file wins over settings.theme)", th.Slot("text"), want.Slot("text"))
	}
}

func TestSettingsThemeUnknownRefusesNamingKnown(t *testing.T) {
	_, err := tui.ResolveTheme("plasma", nil, true)
	if err == nil {
		t.Fatal("settings.theme=plasma: no refusal")
	}
	if !strings.Contains(err.Error(), `theme: unknown value "plasma" (known: oled, p1, p3, paper)`) {
		t.Fatalf("refusal %q does not name the shipped set", err.Error())
	}
}

// --- the 256 downconvert (decision 7's named case) ---

func TestDownconvert256KnownHexes(t *testing.T) {
	cases := []struct {
		hex  string
		want int
	}{
		{"#ff0000", 196}, // cube 5,0,0
		{"#00ff00", 46},  // cube 0,5,0
		{"#0000ff", 21},  // cube 0,0,5
		{"#ff8000", 208}, // cube 5,2,0 (nearest: g 128 -> 135)
		{"#c0c0c0", 250}, // grayscale ramp, nearest step
		{"#ffffff", 231},
		{"#000000", 16},
		{"#585858", 240}, // the ramp step 8+10*8 is exact
	}
	for _, c := range cases {
		if got := tui.Nearest256(c.hex); got != c.want {
			t.Errorf("Nearest256(%s) = %d, want %d", c.hex, got, c.want)
		}
	}
}

func TestDownconvertRefusesBadHex(t *testing.T) {
	for _, bad := range []string{"#ff9e6", "#zzzzzz", "ff9e64", "#ff9e645", ""} {
		if _, _, _, err := tui.ParseHex(bad); err == nil {
			t.Errorf("ParseHex(%q): no refusal", bad)
		}
	}
}

// --- the phosphor ramp (decision 7's named case) ---

func TestPhosphorRampIsFourDistinctBrightnesses(t *testing.T) {
	for _, name := range []string{"p1", "p3"} {
		th, err := tui.ResolveTheme(name, nil, true)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		slots := []string{"text", "dim", "accent", "success", "error", "warn", "rule", "reasoning"}
		seen := map[string]bool{}
		for _, s := range slots {
			seen[th.Slot(s)] = true
		}
		if len(seen) != 4 {
			t.Fatalf("%s: %d distinct slot values, want the four-brightness ramp %v", name, len(seen), seen)
		}
		// the hierarchy is pinned: text brightest, rule/dim deepest,
		// accent+success the next tier, error+warn+reasoning the middle.
		if th.Slot("text") == th.Slot("dim") || th.Slot("accent") == th.Slot("warn") {
			t.Fatalf("%s: the hierarchy collapsed: %v", name, seen)
		}
		if th.Slot("accent") != th.Slot("success") {
			t.Fatalf("%s: accent and success share a tier (%q vs %q)", name, th.Slot("accent"), th.Slot("success"))
		}
		if th.Slot("error") != th.Slot("warn") || th.Slot("warn") != th.Slot("reasoning") {
			t.Fatalf("%s: error/warn/reasoning share a tier", name)
		}
		if th.Slot("dim") != th.Slot("rule") {
			t.Fatalf("%s: dim and rule share the deepest tier", name)
		}
	}
}

// --- SGR rendering (ansi) ---

func TestPaintTrueColorAnd256(t *testing.T) {
	tc, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	// accent #61afef: the truecolor sequence is named, not by eye.
	got := tc.Paint("accent", "bash")
	want := "\x1b[38;2;97;175;239m" + "bash" + "\x1b[0m"
	if got != want {
		t.Fatalf("truecolor paint = %q, want %q", got, want)
	}
	nc, err := tui.ResolveTheme("oled", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	n256 := tui.Nearest256("#61afef")
	if got := nc.Paint("accent", "bash"); got != "\x1b[38;5;"+strconv.Itoa(n256)+"m"+`bash`+"\x1b[0m" {
		t.Fatalf("256 paint = %q, want the downconverted index %d", got, n256)
	}
}
