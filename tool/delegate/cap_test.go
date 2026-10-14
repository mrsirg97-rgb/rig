package delegate

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCapOutputCutsOnARuneBoundaryAndNamesTheTotal(t *testing.T) {
	s := strings.Repeat("a", outputCap-1) + "日" + strings.Repeat("b", 64)
	got := capOutput(s)
	idx := strings.Index(got, "\n[TRUNCATED: ")
	if idx < 0 {
		t.Fatalf("the cap must name its truncation: %q", got[len(got)-80:])
	}
	if !utf8.ValidString(got[:idx]) {
		t.Fatalf("the cut must not split a rune: %q", got[:idx][idx-8:])
	}
	want := "\n[TRUNCATED: " + strconv.Itoa(len(s)) + " bytes total]"
	if got[idx:] != want {
		t.Fatalf("the marker = %q, want %q", got[idx:], want)
	}
}

func TestCapOutputKeepsAShortStringWhole(t *testing.T) {
	if got := capOutput("the answer"); got != "the answer" {
		t.Fatalf("an uncapped string rides verbatim: %q", got)
	}
}
