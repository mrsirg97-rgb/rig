package tui

import (
	"strings"
	"unicode/utf8"
)

type key int

const (
	keyNone key = iota
	keyText
	keyEnter
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyUp
	keyDown
	keyCtrlC
	keyCtrlD
	keyCtrlT
	keyTab
	keyShiftTab
	keyEsc
	keyKillToStart
	keyKillToEnd
	keyWordBack
	keyPasteStart
	keyPasteEnd
	keyPgUp
	keyPgDn
)

const (
	stTop = iota
	stEsc
	stEsc3
	stCSI
	stSS3
	stOSC
	stOSCST
	stUTF8
)

type keyParser struct {
	state   int
	csi     []byte
	utf8buf []byte
	paste   bool
	cr      bool
}

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
			return keyNone, 0
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
			p.state = stOSC
		case b >= 0x20 && b <= 0x2f:

			p.state = stEsc3
		}

	case stEsc3:
		p.state = stTop

	case stCSI:
		if b >= 0x40 && b <= 0x7e {
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
			p.csi = append(p.csi, b)
		}

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
		case 0x07:
			p.state = stTop
		case 0x1b:
			p.state = stOSCST
		}
	case stOSCST:
		p.state = stTop
		if b != '\\' {

			return p.next(b)
		}
	case stTop:
		wasCR := p.cr
		p.cr = false
		switch {
		case b == '\n' || b == '\r':

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

		case b >= 0xc0:
			p.utf8buf = []byte{b}
			p.state = stUTF8
		case p.paste:

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
	case term == 'Z':
		return keyShiftTab
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

type editor struct {
	buf     []rune
	pos     int
	hist    []string
	histPos int
	draft   string
}

func newEditor() editor {
	return editor{histPos: -1}
}

func (e *editor) apply(k key, r rune) (string, bool) {
	switch k {
	case keyText:
		e.buf = append(e.buf[:e.pos], append([]rune{r}, e.buf[e.pos:]...)...)
		e.pos++
	case keyBackspace:

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

		e.buf, e.pos = nil, 0
		e.histPos = -1
		e.draft = ""
	case keyKillToStart:
		e.buf = append([]rune{}, e.buf[e.pos:]...)
		e.pos = 0
	case keyKillToEnd:
		e.buf = e.buf[:e.pos]
	case keyWordBack:

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
			return "", false
		}
		e.hist = append(e.hist, line)
		return line, true
	}
	return "", false
}

func (e *editor) setText(t string) {
	e.buf = []rune(t)
	e.pos = len(e.buf)
}

func (e *editor) text() string { return string(e.buf) }

func (e *editor) cursorCol(prefixCols int) int {
	return prefixCols + runeWidthSum(string(e.buf[:e.pos])) + 1
}
