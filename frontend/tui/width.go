package tui

import (
	"regexp"

	"github.com/mattn/go-runewidth"
)

// The width table is go-runewidth's (decision 10: hand-rolling width
// tables is the known wrong move). The rest of the package goes through
// these two wrappers so the dependency stays a leaf of this package.

func runeWidth(r rune) int { return runewidth.RuneWidth(r) }

func runeWidthSum(s string) int { return runewidth.StringWidth(s) }

var sgrRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// WidthOf is the visible display width of a painted line: the SGR
// escapes are the paint, not the text (the tests measure against the
// terminal's columns this way).
func WidthOf(s string) int { return runeWidthSum(sgrRe.ReplaceAllString(s, "")) }
