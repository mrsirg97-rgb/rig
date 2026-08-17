package command_test

import (
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
)

// TestParseCommandShape (SPEC_COMMANDS, named): the name is the first
// token; the args are the remainder after that one separator run — the
// leading run of blanks stripped, the interior verbatim.
func TestParseCommandShape(t *testing.T) {
	cases := []struct {
		line, name, args string
	}{
		{"/compact", "compact", ""},
		{"/compact now", "compact", "now"},
		{"/compact   a  b", "compact", "a  b"},
		{"/compact\tnow", "compact", "now"},
		{"/x y z", "x", "y z"},
		{"/steer fix  it", "steer", "fix  it"},
	}
	for _, c := range cases {
		name, args := command.Parse(c.line)
		if name != c.name || args != c.args {
			t.Fatalf("Parse(%q) = (%q, %q), want (%q, %q)", c.line, name, args, c.name, c.args)
		}
	}
}

// TestParsePrefixEdge (SPEC_COMMANDS, named): '/' alone is the command
// line with the empty name; '//' lines are escaped prompts, the escape
// consuming one slash.
func TestParsePrefixEdge(t *testing.T) {
	if !command.IsCommandLine("/") {
		t.Fatal("'/' alone must be a command line (the empty name)")
	}
	name, args := command.Parse("/")
	if name != "" || args != "" {
		t.Fatalf("Parse('/') = (%q, %q), want the empty name", name, args)
	}

	if command.IsCommandLine("//home/ng") {
		t.Fatal("'//home/ng' must not be a command line")
	}
	if got := command.Unescape("//home/ng"); got != "/home/ng" {
		t.Fatalf("the escape must consume one slash: %q", got)
	}
	if got := command.Unescape("///x"); got != "//x" {
		t.Fatalf("the escape must consume exactly one slash: %q", got)
	}

	if command.IsCommandLine("") {
		t.Fatal("the empty line is not a command line")
	}
	if command.IsCommandLine("compact") {
		t.Fatal("a bare word is not a command line (exact-word matching was rejected)")
	}
}
