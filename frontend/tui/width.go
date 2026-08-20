package tui

import (
	"regexp"

	"github.com/mattn/go-runewidth"
)

func runeWidth(r rune) int { return runewidth.RuneWidth(r) }

func runeWidthSum(s string) int { return runewidth.StringWidth(s) }

var sgrRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func WidthOf(s string) int { return runeWidthSum(sgrRe.ReplaceAllString(s, "")) }
