package tui_test

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/frontend/tui"
)

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
	if th.Name() != "warm" {
		t.Fatalf("default = %q, want warm (oled's table under its preset name)", th.Name())
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
	as, err := tui.ResolveTheme("", json.RawMessage(doc), true)
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

func TestThemeJSONSchemaRefusals(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"unknown base", `{"base":"plasma"}`, `unknown base "plasma" (known: cool, oled, p1, p3, paper, warm)`},
		{"missing base", `{"glyphs":"ascii"}`, `base required (known: cool, oled, p1, p3, paper, warm)`},
		{"unknown slot", `{"base":"oled","slots":{"hue":"#ff9e64"}}`, `unknown slot "hue" (known: accent, dim, effortHigh, effortLow, effortMax, effortMedium, effortMinimal, effortOff, effortXhigh, ember, error, reasoning, rule, success, text, warn)`},
		{"bad hex short", `{"base":"oled","slots":{"accent":"#ff9e6"}}`, `accent: #ff9e6: expected #rrggbb`},
		{"bad hex digits", `{"base":"oled","slots":{"accent":"#ffzz64"}}`, `accent: #ffzz64: expected #rrggbb`},
		{"bad hex no hash", `{"base":"oled","slots":{"accent":"ff9e64"}}`, `accent: ff9e64: expected #rrggbb`},
		{"slot not a string", `{"base":"oled","slots":{"accent":19801700}}`, `accent: expected a string, got 19801700`},
		{"slots not an object", `{"base":"oled","slots":["accent"]}`, `slots: expected an object, got array`},
		{"bad glyphs", `{"base":"oled","glyphs":"emoji"}`, `glyphs: unknown "emoji" (known: ascii, unicode)`},
		{"unknown key", `{"base":"oled","palette":"oled"}`, `unknown key "palette" (known: base, glyphs, slots)`},
		{"not an object", `[1,2,3]`, `theme.json: expected a JSON object`},
		{"empty object missing base", `{}`, `base required (known: cool, oled, p1, p3, paper, warm)`},
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
	th, err := tui.ResolveTheme("", json.RawMessage(doc), true)
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

func TestThemeJSONAloneIsTheLegacyPath(t *testing.T) {

	doc := []byte(`{"base":"p3"}`)
	th, err := tui.ResolveTheme("", json.RawMessage(doc), true)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := tui.ResolveTheme("p3", nil, true)
	if th.Slot("text") != want.Slot("text") {
		t.Fatalf("text = %q, want p3's %q (with no dial the file is the theme)", th.Slot("text"), want.Slot("text"))
	}
}

func TestSettingsThemeUnknownRefusesNamingKnown(t *testing.T) {
	_, err := tui.ResolveTheme("plasma", nil, true)
	if err == nil {
		t.Fatal("settings.theme=plasma: no refusal")
	}
	if !strings.Contains(err.Error(), `theme: unknown value "plasma" (known: cool, oled, p1, p3, paper, warm)`) {
		t.Fatalf("refusal %q does not name the shipped set", err.Error())
	}
}

func TestDownconvert256KnownHexes(t *testing.T) {
	cases := []struct {
		hex  string
		want int
	}{
		{"#ff0000", 196},
		{"#00ff00", 46},
		{"#0000ff", 21},
		{"#ff8000", 208},
		{"#c0c0c0", 250},
		{"#ffffff", 231},
		{"#000000", 16},
		{"#585858", 240},
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

func TestPaintTrueColorAnd256(t *testing.T) {
	tc, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}

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

func TestEmberBreath(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := th.EmberPaint(0, "thinking"); got != th.Paint("ember", "thinking") {
		t.Fatalf("the breath starts at the ember, got %q", got)
	}
	emberR, emberG, emberB := sgrRGB(t, th.Paint("ember", "x"))
	darkR, darkG, darkB := sgrRGB(t, th.EmberPaint(6, "x"))
	if darkR >= emberR || darkG >= emberG || darkB >= emberB {
		t.Fatalf("the darkest stop must be darker than the ember: %d,%d,%d vs %d,%d,%d", darkR, darkG, darkB, emberR, emberG, emberB)
	}
	if diff := abs(darkR*emberG - emberR*darkG); diff*100 > 5*emberR*darkG {
		t.Fatalf("the darkest stop keeps the ember's hue: %d,%d,%d", darkR, darkG, darkB)
	}
	for i := 0; i < 12; i++ {
		p := th.EmberPaint(i, "thinking")
		if tui.RemoveColor(p) != "thinking" {
			t.Fatalf("the label is never split or glyph-prefixed: %q", p)
		}
		if strings.Count(p, "38;2;") != 1 {
			t.Fatalf("one colour per frame, never per character: %q", p)
		}
		if len(tui.RemoveColor(p)) != len("thinking") {
			t.Fatalf("every frame is the same width: %q", p)
		}
	}
	prev := th.EmberPaint(0, "x")
	for i := 1; i <= 6; i++ {
		cur := th.EmberPaint(i, "x")
		cr, cg, cb := sgrRGB(t, cur)
		pr, pg, pb := sgrRGB(t, prev)
		if cr >= pr || cg >= pg || cb >= pb {
			t.Fatalf("the breath descends: stop %d (%d,%d,%d) is not darker than stop %d (%d,%d,%d)", i, cr, cg, cb, i-1, pr, pg, pb)
		}
		prev = cur
	}
	for i := 1; i <= 5; i++ {
		if th.EmberPaint(12-i, "x") != th.EmberPaint(i, "x") {
			t.Fatalf("the breath returns along the same sine: stop %d != stop %d", 12-i, i)
		}
	}
	if th.EmberPaint(12, "thinking") != th.EmberPaint(0, "thinking") {
		t.Fatalf("the breath wraps at twelve stops")
	}
}

func TestEmberBreathCollapseTogglesTwoStops(t *testing.T) {
	th, err := tui.ResolveTheme("", json.RawMessage(`{"base":"oled","slots":{"ember":"#101010"}}`), false)
	if err != nil {
		t.Fatal(err)
	}
	a, b := th.EmberPaint(0, "thinking"), th.EmberPaint(1, "thinking")
	if a == b {
		t.Fatalf("collapsed stops must still toggle two colours: %q", a)
	}
	if th.EmberPaint(2, "thinking") != a || th.EmberPaint(3, "thinking") != b {
		t.Fatalf("the two-stop toggle alternates at the same cadence")
	}
	nc, err := tui.ResolveTheme("oled", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for i := 0; i < 12; i++ {
		seen[nc.EmberPaint(i, "x")] = true
	}
	if len(seen) < 3 {
		t.Fatalf("an unconverted-down palette keeps at least three stops: %v", seen)
	}
}

func sgrRGB(t *testing.T, s string) (int, int, int) {
	t.Helper()
	m := regexp.MustCompile(`\x1b\[38;2;(\d+);(\d+);(\d+)m`).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("no truecolor SGR in %q", s)
	}
	return atoiTest(m[1]), atoiTest(m[2]), atoiTest(m[3])
}

func atoiTest(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func TestWarmShipsTheDefaultPalette(t *testing.T) {
	th, err := tui.ResolveTheme("warm", nil, true)
	if err != nil {
		t.Fatalf("ResolveTheme(warm): %v", err)
	}
	if th.Name() != "warm" {
		t.Fatalf("name = %q, want warm", th.Name())
	}
	for slot, want := range map[string]string{
		"text": "#d8d8d8", "dim": "#6e6e6e", "accent": "#61afef", "success": "#98c379",
		"error": "#e06c75", "warn": "#e5c07b", "rule": "#3c3c3c", "reasoning": "#8a8a8a",
		"ember": "#e8a86b", "effortOff": "#5a5a5a", "effortXhigh": "#d183e8", "effortMax": "#ff5fff",
	} {
		if got := th.Slot(slot); got != want {
			t.Fatalf("warm slot %s = %q, want %q", slot, got, want)
		}
	}
}

func TestCoolShipsTheCoolPalette(t *testing.T) {
	th, err := tui.ResolveTheme("cool", nil, true)
	if err != nil {
		t.Fatalf("ResolveTheme(cool): %v", err)
	}
	if th.Name() != "cool" {
		t.Fatalf("name = %q, want cool", th.Name())
	}
	for slot, want := range map[string]string{
		"text": "#b8bfcc", "dim": "#3a4150", "accent": "#8a9bbd", "success": "#6fa38a",
		"error": "#a36f6f", "warn": "#a39a6f", "rule": "#2a303b", "reasoning": "#4f5868",
		"ember": "#6b7fa3", "effortOff": "#3a4150", "effortMax": "#ff5fff",
	} {
		if got := th.Slot(slot); got != want {
			t.Fatalf("cool slot %s = %q, want %q", slot, got, want)
		}
	}
}

func TestOledStaysTheLegacyAlias(t *testing.T) {
	warm, err := tui.ResolveTheme("warm", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	oled, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatalf("the legacy name must keep resolving: %v", err)
	}
	for _, slot := range []string{"text", "dim", "accent", "ember", "effortLow"} {
		if oled.Slot(slot) != warm.Slot(slot) {
			t.Fatalf("oled slot %s = %q, want warm's %q", slot, oled.Slot(slot), warm.Slot(slot))
		}
	}
}

func TestSettingsKeyBeatsTheFile(t *testing.T) {
	doc := json.RawMessage(`{"base": "paper", "slots": {"accent": "#ff0000"}}`)
	th, err := tui.ResolveTheme("cool", doc, true)
	if err != nil {
		t.Fatalf("ResolveTheme: %v", err)
	}
	if th.Name() != "cool" || th.Slot("accent") != "#8a9bbd" {
		t.Fatalf("the settings dial must name the theme alone, got %s %s", th.Name(), th.Slot("accent"))
	}
}

func TestCustomWithoutTheFileRefusesLoud(t *testing.T) {
	_, err := tui.ResolveTheme("custom", nil, true)
	if err == nil || !strings.Contains(err.Error(), "theme.json") {
		t.Fatalf("custom without the file = %v, want a loud refusal naming theme.json", err)
	}
}

func TestCustomResolvesTheFile(t *testing.T) {
	doc := json.RawMessage(`{"base": "warm", "slots": {"accent": "#ff9e64"}, "glyphs": "ascii"}`)
	th, err := tui.ResolveTheme("custom", doc, true)
	if err != nil {
		t.Fatalf("ResolveTheme(custom): %v", err)
	}
	if th.Name() != "warm" || th.Slot("accent") != "#ff9e64" || th.Glyph("prompt") != ">" {
		t.Fatalf("the custom theme = %s %s %q, want the file's base, slot, and glyphs", th.Name(), th.Slot("accent"), th.Glyph("prompt"))
	}
}
