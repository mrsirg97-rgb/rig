package tui

import (
	"strings"
	"unicode/utf8"
)

// key is one parsed input event (decision 9's named vocabulary).
type key int

const (
	keyNone key = iota
	keyText     // the rune carries it
	keyEnter
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyUp   // history
	keyDown // history
	keyCtrlC
	keyCtrlD
	keyCtrlT
	keyTab
	keyEsc         // a lone escape: clears the prompt (the reader names it, not the parser)
	keyKillToStart // Ctrl-U
	keyKillToEnd   // Ctrl-K
	keyWordBack    // Ctrl-W
	keyPasteStart  // CSI 200~: the bracketed paste opens (parser-internal)
	keyPasteEnd    // CSI 201~: the bracketed paste closes (parser-internal)
	keyPgUp        // opens the pager; pages inside it
	keyPgDn        // pages inside the pager
)

// The parser states: top, the escape families (CSI, the SS3 cursor
// mode, the OSC device string), and a UTF-8 accumulation.
const (
	stTop = iota
	stEsc
	stEsc3 // an ESC + intermediate: the designator's final byte is consumed
	stCSI
	stSS3
	stOSC
	stOSCST // an ESC inside the OSC: the ST's backslash or content
	stUTF8
)

// keyParser is the raw-mode key parser (decision 9): the named keys —
// the arrows, home/end, backspace and delete across the line, Enter,
// Ctrl-C/D/T, and printable text — and every other sequence is consumed
// and ignored, never a crash on an exotic terminal. It is the only
// state the reader goroutine keeps on the byte stream.
type keyParser struct {
	state   int
	csi     []byte
	utf8buf []byte
	paste   bool // inside a bracketed paste: newlines are text, controls inert
	cr      bool // the last paste byte was CR (a CRLF folds to one newline)
}

// next feeds one byte and reports the key it completed, if any. A text
// key carries its rune; the multi-byte ones arrive as a single key, so
// the editor sees a wide glyph as one unit (the backspace's rule).
func (p *keyParser) next(b byte) (key, rune) {
	switch p.state {
	case stUTF8:
		p.utf8buf = append(p.utf8buf, b)
		if len(p.utf8buf) < utf8Len(p.utf8buf[0]) {
			return keyNone, 0
		}
		p.state = stTop
		r, size := utf8.DecodeRune(p.utf8buf)
		buf := p.utf8buf
		p.utf8buf = nil
		if r == utf8.RuneError && (size < len(buf) || size == 1) {
			return keyNone, 0 // an invalid sequence: consumed, dropped
		}
		return keyText, r
	case stEsc:
		p.state = stTop
		switch {
		case b == '[':
			p.state = stCSI
			p.csi = nil
		case b == 'O':
			p.state = stSS3
		case b == ']':
			p.state = stOSC // a device string (a title, a paste artifact): the BEL or ST ends it
		case b >= 0x20 && b <= 0x2f:
			// a designator (a charset selection, a mode): the final
			// byte is part of the sequence, consumed.
			p.state = stEsc3
		}
		// a lone ESC or another two-byte sequence: not named, consumed.
	case stEsc3:
		p.state = stTop // the final byte: consumed (0x30-0x7e or not)

	case stCSI:
		if b >= 0x40 && b <= 0x7e { // the terminating byte
			p.state = stTop
			switch k := csiKey(string(p.csi), b); k {
			case keyPasteStart:
				p.paste = true
				return keyNone, 0
			case keyPasteEnd:
				p.paste, p.cr = false, false
				return keyNone, 0
			default:
				return k, 0
			}
		}
		if b >= 0x20 && b <= 0x3f && len(p.csi) < 32 {
			p.csi = append(p.csi, b) // a parameter (0x30-0x3f) or intermediate (0x20-0x2f) byte
		}
		// a malformed middle byte: consumed (the length cap bounds it)
	case stSS3:
		p.state = stTop
		switch b {
		case 'A':
			return keyUp, 0
		case 'B':
			return keyDown, 0
		case 'C':
			return keyRight, 0
		case 'D':
			return keyLeft, 0
		}
	case stOSC:
		switch b {
		case 0x07: // BEL: the string ends
			p.state = stTop
		case 0x1b: // the ST candidate: the next byte decides
			p.state = stOSCST
		}
	case stOSCST:
		p.state = stTop
		if b != '\\' {
			// not the ST: the ESC was content; re-feed this byte at top.
			return p.next(b)
		}
	case stTop:
		wasCR := p.cr
		p.cr = false
		switch {
		case b == '\n' || b == '\r':
			// inside a bracketed paste, a newline is text (the paste is
			// one input); a CRLF folds to one. Typed, it is the Enter.
			if p.paste {
				if b == '\n' && wasCR {
					return keyNone, 0
				}
				p.cr = b == '\r'
				return keyText, '\n'
			}
			return keyEnter, 0
		case b == 0x09:
			if p.paste {
				return keyText, '\t'
			}
			return keyTab, 0
		case b == 0x1b:
			p.state = stEsc
		case b >= 0x20 && b < 0x7f:
			return keyText, rune(b)
		case b >= 0x80 && b < 0xc0:
			// an orphaned continuation byte: consumed, dropped.
		case b >= 0xc0:
			p.utf8buf = []byte{b}
			p.state = stUTF8
		case p.paste:
			// a pasted control byte: consumed, never an action.
		case b == 0x7f || b == 0x08:
			return keyBackspace, 0
		case b == 0x03:
			return keyCtrlC, 0
		case b == 0x04:
			return keyCtrlD, 0
		case b == 0x14:
			return keyCtrlT, 0
		case b == 0x01:
			return keyHome, 0
		case b == 0x05:
			return keyEnd, 0
		case b == 0x15:
			return keyKillToStart, 0
		case b == 0x0b:
			return keyKillToEnd, 0
		case b == 0x17:
			return keyWordBack, 0
		}
		return keyNone, 0
	}
	return keyNone, 0
}

// csiKey is the named CSI set: the arrows (history and the line's
// horizontal cursor), home/end (all the common encodings), delete.
// Every other CSI is the unrecognized case, ignored (9).
func csiKey(params string, term byte) key {
	switch {
	case term == 'A':
		return keyUp
	case term == 'B':
		return keyDown
	case term == 'C':
		return keyRight
	case term == 'D':
		return keyLeft
	case term == 'H':
		return keyHome
	case term == 'F':
		return keyEnd
	case term == '~':
		switch params {
		case "1", "7":
			return keyHome
		case "4", "8":
			return keyEnd
		case "3":
			return keyDelete
		case "5":
			return keyPgUp
		case "6":
			return keyPgDn
		case "200":
			return keyPasteStart
		case "201":
			return keyPasteEnd
		}
	}
	return keyNone
}

// utf8Len is the sequence length of a lead byte (0 for a non-lead).
func utf8Len(b byte) int {
	switch {
	case b >= 0xc2 && b < 0xe0:
		return 2
	case b >= 0xe0 && b < 0xf0:
		return 3
	case b >= 0xf0 && b < 0xf5:
		return 4
	default:
		return 1
	}
}

// editor is the single-line state (decision 9): the rune buffer, the
// edit position, and the session history (in memory, up and down, the
// draft preserved around a history trip). A key applies in place; the
// Enter reports the submitted line. The painting is the TUI's — the
// editor is text and columns only, so it is testable without a
// terminal.
type editor struct {
	buf     []rune
	pos     int
	hist    []string
	histPos int // -1 = the draft
	draft   string
}

// newEditor is the editor's only constructor: the zero value's
// histPos 0 is a valid history index, not "the draft" — the draft
// state (-1) is constructed, never assumed.
func newEditor() editor {
	return editor{histPos: -1}
}

// apply one parsed key. A keyEnter with a non-blank line reports true
// and the submitted text; the line is blank or the key a no-op, false.
func (e *editor) apply(k key, r rune) (string, bool) {
	switch k {
	case keyText:
		e.buf = append(e.buf[:e.pos], append([]rune{r}, e.buf[e.pos:]...)...)
		e.pos++
	case keyBackspace:
		// the rune under the cursor goes, wide glyph whole: the buffer
		// is runes, not bytes (the wide-glyph rule).
		if e.pos > 0 {
			e.buf = append(e.buf[:e.pos-1], e.buf[e.pos:]...)
			e.pos--
		}
	case keyDelete:
		if e.pos < len(e.buf) {
			e.buf = append(e.buf[:e.pos], e.buf[e.pos+1:]...)
		}
	case keyLeft:
		if e.pos > 0 {
			e.pos--
		}
	case keyRight:
		if e.pos < len(e.buf) {
			e.pos++
		}
	case keyHome:
		e.pos = 0
	case keyEnd:
		e.pos = len(e.buf)
	case keyEsc:
		// cancels the prompt: the line is dropped, the history trip
		// abandoned (the draft with it — the Esc is the discard).
		e.buf, e.pos = nil, 0
		e.histPos = -1
		e.draft = ""
	case keyKillToStart:
		e.buf = append([]rune{}, e.buf[e.pos:]...)
		e.pos = 0
	case keyKillToEnd:
		e.buf = e.buf[:e.pos]
	case keyWordBack:
		// the word before the cursor: trailing spaces, then the run of
		// non-spaces (readline's unix-word-rubout).
		i := e.pos
		for i > 0 && e.buf[i-1] == ' ' {
			i--
		}
		for i > 0 && e.buf[i-1] != ' ' {
			i--
		}
		e.buf = append(e.buf[:i], e.buf[e.pos:]...)
		e.pos = i
	case keyUp:
		// the newest entry first (the last submitted), then older —
		// the editor's history order.
		if len(e.hist) == 0 {
			return "", false
		}
		if e.histPos == -1 {
			e.draft = string(e.buf)
			e.histPos = len(e.hist) - 1
		} else if e.histPos > 0 {
			e.histPos--
		}
		e.buf = []rune(e.hist[e.histPos])
		e.pos = len(e.buf)
	case keyDown:
		if e.histPos == -1 {
			return "", false
		}
		if e.histPos < len(e.hist)-1 {
			e.histPos++
		} else {
			e.histPos = -1
			e.buf = []rune(e.draft)
			e.pos = len(e.buf)
			return "", false
		}
		e.buf = []rune(e.hist[e.histPos])
		e.pos = len(e.buf)
	case keyEnter:
		line := string(e.buf)
		e.buf, e.pos = nil, 0
		e.histPos = -1
		e.draft = ""
		if strings.TrimSpace(line) == "" {
			return "", false // a blank line is not a submission
		}
		e.hist = append(e.hist, line)
		return line, true
	}
	return "", false
}

// text is the line's content; cursorCol is the one-based terminal
// column of the edit position, over the painted line's prefix (the
// prompt and its space, prefixCols wide).
// setText replaces the buffer (tab completion's landing) and parks the
// cursor at the end.
func (e *editor) setText(t string) {
	e.buf = []rune(t)
	e.pos = len(e.buf)
}

func (e *editor) text() string { return string(e.buf) }

func (e *editor) cursorCol(prefixCols int) int {
	return prefixCols + runeWidthSum(string(e.buf[:e.pos])) + 1
}
