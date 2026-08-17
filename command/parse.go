package command

import "strings"

// IsCommandLine is the prefix rule (decision 1): a line whose first byte
// is '/' and whose second is not '/' is a command line, full stop — it is
// never a prompt, whatever it says.
func IsCommandLine(line string) bool {
	if len(line) == 0 || line[0] != '/' {
		return false
	}
	return len(line) == 1 || line[1] != '/'
}

// Unescape is the escape, named: '//' + the rest is an escaped prompt,
// and the escape consumes one slash, so the model sees '/' + the rest —
// the cost is one extra slash on a line that would otherwise be hijacked.
func Unescape(line string) string {
	return line[1:]
}

// Parse splits a command line: the name is the first token (up to the
// first space or tab); the args are the remainder after that one
// separator run — the leading run of blanks stripped, everything after
// verbatim.
func Parse(line string) (name, args string) {
	rest := line[1:]
	i := 0
	for i < len(rest) && rest[i] != ' ' && rest[i] != '\t' {
		i++
	}
	name = rest[:i]
	args = strings.TrimLeft(rest[i:], " \t")
	return name, args
}
