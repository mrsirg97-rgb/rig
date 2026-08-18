package tui

import "github.com/mattn/go-runewidth"

// The width table is go-runewidth's (decision 10: hand-rolling width
// tables is the known wrong move). The rest of the package goes through
// these two wrappers so the dependency stays a leaf of this package.

func runeWidth(r rune) int { return runewidth.RuneWidth(r) }

func runeWidthSum(s string) int { return runewidth.StringWidth(s) }
