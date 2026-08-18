package tui

import (
	"testing"
)

// parseBytes runs the named key sequence through the parser and returns
// the keys it reports (the keyNone gaps dropped) — the parser's table.
func parseBytes(t *testing.T, b []byte) []key {
	t.Helper()
	var p keyParser
	var out []key
	for _, c := range b {
		if k, _ := p.next(c); k != keyNone {
			out = append(out, k)
		}
	}
	return out
}

func eqKeys(got, want []key) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestKeyParserTable is the named input case (SPEC_TUI's testing
// section): the arrows, home/end (every common encoding), backspace,
// delete, the control keys, and the unrecognized sequences consumed and
// ignored — never a crash on an exotic terminal.
func TestKeyParserTable(t *testing.T) {
	cases := []struct {
		name  string
		bytes []byte
		want  []key
	}{
		{"printable", []byte("abc"), []key{keyText, keyText, keyText}},
		{"enter cr", []byte("a\r"), []key{keyText, keyEnter}},
		{"enter lf", []byte("a\n"), []key{keyText, keyEnter}},
		{"backspace del", []byte("a\x7f"), []key{keyText, keyBackspace}},
		{"backspace bs", []byte("a\x08"), []key{keyText, keyBackspace}},
		{"ctrl c", []byte{0x03}, []key{keyCtrlC}},
		{"ctrl d", []byte{0x04}, []key{keyCtrlD}},
		{"ctrl t", []byte{0x14}, []key{keyCtrlT}},
		{"ctrl a home", []byte{0x01}, []key{keyHome}},
		{"ctrl e end", []byte{0x05}, []key{keyEnd}},
		{"arrow up csi", []byte{0x1b, '[', 'A'}, []key{keyUp}},
		{"arrow up ss3", []byte{0x1b, 'O', 'A'}, []key{keyUp}},
		{"arrow down csi", []byte{0x1b, '[', 'B'}, []key{keyDown}},
		{"arrow left ss3", []byte{0x1b, 'O', 'D'}, []key{keyLeft}},
		{"arrow right csi", []byte{0x1b, '[', 'C'}, []key{keyRight}},
		{"home plain", []byte{0x1b, '[', 'H'}, []key{keyHome}},
		{"home tilde 1", []byte{0x1b, '[', '1', '~'}, []key{keyHome}},
		{"home tilde 7", []byte{0x1b, '[', '7', '~'}, []key{keyHome}},
		{"home params", []byte{0x1b, '[', '1', ';', '5', 'H'}, []key{keyHome}},
		{"end plain", []byte{0x1b, '[', 'F'}, []key{keyEnd}},
		{"end tilde 4", []byte{0x1b, '[', '4', '~'}, []key{keyEnd}},
		{"end tilde 8", []byte{0x1b, '[', '8', '~'}, []key{keyEnd}},
		{"delete tilde", []byte{0x1b, '[', '3', '~'}, []key{keyDelete}},
		// the unrecognized set: consumed, ignored, the line untouched.
		{"page up", []byte{0x1b, '[', '5', '~'}, []key{keyPgUp}},
		{"page down", []byte{0x1b, '[', '6', '~'}, []key{keyPgDn}},
		{"unrecognized tilde", []byte{0x1b, '[', '1', '5', '~'}, []key{}},
		{"unrecognized shift tab", []byte{0x1b, '[', 'Z'}, []key{}},
		{"two byte escape", []byte{0x1b, '(', 'B'}, []key{}},
		// a byte after the two-byte mode selection is its own content:
		// the parser consumes the mode, the text stands.
		// the designator's final byte is part of the sequence; the
		// text after it stands on its own.
		{"two byte escape plus text", []byte{0x1b, '(', 'B', 'a'}, []key{keyText}},
		{"lone escape", []byte{0x1b}, []key{}},
		{"osc title", []byte{0x1b, ']', '0', ';', 'r', 'i', 'g', 0x07}, []key{}},
		{"osc string terminator", []byte{0x1b, ']', '0', ';', 'x', 0x1b, '\\'}, []key{}},
		{"tab control", []byte{0x09}, []key{keyTab}},
		{"ctrl u kill to start", []byte{0x15}, []key{keyKillToStart}},
		{"ctrl k kill to end", []byte{0x0b}, []key{keyKillToEnd}},
		{"ctrl w word back", []byte{0x17}, []key{keyWordBack}},
		{"paste newlines are text", []byte("\x1b[200~a\nb\x1b[201~\n"),
			[]key{keyText, keyText, keyText, keyEnter}},
		{"paste crlf folds", []byte("\x1b[200~a\r\nb\x1b[201~"),
			[]key{keyText, keyText, keyText}},
		{"paste controls inert", []byte("\x1b[200~a\x03\x7f\x1b[201~"),
			[]key{keyText}},
		{"paste tab is text", []byte("\x1b[200~\ta\x1b[201~"),
			[]key{keyText, keyText}},
		{"orphan continuation", []byte{0x61, 0x80}, []key{keyText}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseBytes(t, tc.bytes); !eqKeys(got, tc.want) {
				t.Fatalf("parsed %v, want %v", got, tc.want)
			}
		})
	}
}

// TestKeyParserWideGlyph is the wide-glyph rule: the multi-byte rune
// arrives as a single key, so the editor and the backspace see it as
// one unit.
func TestKeyParserWideGlyph(t *testing.T) {
	var p keyParser
	var keys []key
	var runes []rune
	for i := 0; i < len("ab你"); i++ {
		k, r := p.next([]byte("ab你")[i])
		if k == keyText {
			keys = append(keys, k)
			runes = append(runes, r)
		}
	}
	if len(keys) != 3 || runes[2] != '你' {
		t.Fatalf("the wide glyph must arrive as one key: keys=%d runes=%v", len(keys), runes)
	}
}

// TestEditorBackspaceAcrossWideGlyph: the rune under the cursor goes,
// wide glyph whole — the buffer is runes, not bytes.
func TestEditorBackspaceAcrossWideGlyph(t *testing.T) {
	var e editor
	e.buf = []rune("ab你cd")
	e.pos = 3 // after the wide glyph
	e.apply(keyBackspace, 0)
	if e.text() != "abcd" || e.pos != 2 {
		t.Fatalf("backspace across the wide glyph: buf=%q pos=%d, want abcd 2", e.text(), e.pos)
	}
	// delete, the forward edge of the same rule.
	e.buf = []rune("ab你cd")
	e.pos = 2
	e.apply(keyDelete, 0)
	if e.text() != "abcd" {
		t.Fatalf("delete across the wide glyph: %q, want abcd", e.text())
	}
}

// TestEditorCursorAcrossWideGlyph: the edit column is runewidth's — a
// wide glyph is two columns in the terminal, and the cursor parks
// after both.
func TestEditorCursorAcrossWideGlyph(t *testing.T) {
	var e editor
	e.buf = []rune("你x")
	e.pos = 2
	if got := e.cursorCol(2); got != 6 {
		t.Fatalf("the cursor column over a wide glyph = %d, want 6 (prefix 2 + 2 + 1)", got)
	}
}

// TestEditorHistoryUpDown is the named input case: the session history,
// up and down, the draft preserved around the trip.
func TestEditorHistoryUpDown(t *testing.T) {
	e := newEditor()
	e.buf = []rune("draft")
	e.pos = 5
	// hist in submission order: "second" is the newest.
	e.hist = []string{"first", "second"}

	// up from the draft: the newest entry, the draft saved.
	e.apply(keyUp, 0)
	if e.text() != "second" {
		t.Fatalf("up from the draft = %q, want the newest entry", e.text())
	}
	// up again: the older one.
	e.apply(keyUp, 0)
	if e.text() != "first" {
		t.Fatalf("up again = %q, want the older entry", e.text())
	}
	// up at the top: stays.
	e.apply(keyUp, 0)
	if e.text() != "first" {
		t.Fatalf("up at the top moved: %q", e.text())
	}
	// down: back toward the draft.
	e.apply(keyDown, 0)
	if e.text() != "second" {
		t.Fatalf("down = %q, want the newer entry", e.text())
	}
	e.apply(keyDown, 0)
	if e.text() != "draft" {
		t.Fatalf("down at the newest = %q, want the draft", e.text())
	}
	// down past the draft: stays.
	e.apply(keyDown, 0)
	if e.text() != "draft" {
		t.Fatalf("down past the draft moved: %q", e.text())
	}
	// Enter adds the submitted line to the history.
	line, submitted := e.apply(keyEnter, 0)
	if !submitted || line != "draft" {
		t.Fatalf("Enter = (%q, %v), want the draft submitted", line, submitted)
	}
	if len(e.hist) != 3 || e.hist[2] != "draft" {
		t.Fatalf("Enter did not record the draft: %v", e.hist)
	}
	// a blank line is not recorded, and not submitted.
	_, submitted = e.apply(keyEnter, 0)
	if submitted {
		t.Fatal("the blank line was submitted")
	}
	if len(e.hist) != 3 {
		t.Fatalf("the blank line was recorded: %v", e.hist)
	}
}

// TestEditorKillOps (decision 9's keybinds): Ctrl-U to the start,
// Ctrl-K to the end, Ctrl-W the word before the cursor, Esc the whole
// prompt (the history draft with it).
func TestEditorKillOps(t *testing.T) {
	e := newEditor()
	feed := func(s string) {
		for _, r := range s {
			e.apply(keyText, r)
		}
	}
	feed("alpha beta  gamma")
	e.apply(keyWordBack, 0)
	if got := e.text(); got != "alpha beta  " {
		t.Fatalf("after ^W = %q, want the last word gone", got)
	}
	e.apply(keyWordBack, 0)
	if got := e.text(); got != "alpha " {
		t.Fatalf("after ^W ^W = %q, want the spaces then the word gone", got)
	}
	e.apply(keyKillToStart, 0)
	if got := e.text(); got != "" || e.pos != 0 {
		t.Fatalf("after ^U = %q pos %d, want empty at 0", got, e.pos)
	}
	feed("keep tail")
	e.apply(keyHome, 0)
	for i := 0; i < 4; i++ {
		e.apply(keyRight, 0)
	}
	e.apply(keyKillToEnd, 0)
	if got := e.text(); got != "keep" {
		t.Fatalf("after ^K = %q, want keep", got)
	}
	e.apply(keyEsc, 0)
	if got := e.text(); got != "" || e.pos != 0 {
		t.Fatalf("after Esc = %q pos %d, want the prompt cancelled", got, e.pos)
	}
}
