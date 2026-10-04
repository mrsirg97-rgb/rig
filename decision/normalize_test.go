package decision

import "testing"

func TestNormalizeStripsOneLeadingCdAndCollapsesWhitespace(t *testing.T) {
	for _, c := range []struct{ name, command, want string }{
		{"cd and", "cd /tmp && rm -rf x", "rm -rf x"},
		{"cd semicolon", "cd /tmp; rm -rf x", "rm -rf x"},
		{"cd semicolon spaced", "cd /tmp ; rm -rf x", "rm -rf x"},
		{"one leading strip", "cd /tmp && cd /var && ls", "cd /var && ls"},
		{"cd runs collapse", "cd   /tmp  &&   ls   -la", "ls -la"},
		{"cd trim", "  cd /tmp && ls -la  ", "ls -la"},
		{"no cd", "ls -la", "ls -la"},
		{"bare cd stays", "cd /tmp", "cd /tmp"},
		{"cd home stays", "cd && ls", "cd && ls"},
		{"cd without a follow-up", "cd /tmp &&", ""},
		{"runs inside collapse", "rm   -rf   /tmp/x", "rm -rf /tmp/x"},
		{"tabs and newlines", "cd /tmp &&\nls\t-la", "ls -la"},
	} {
		if got := normalize(c.command); got != c.want {
			t.Errorf("%s: normalize(%q) = %q, want %q", c.name, c.command, got, c.want)
		}
	}
}
