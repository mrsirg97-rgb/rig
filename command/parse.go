package command

import "strings"

func IsCommandLine(line string) bool {
	if len(line) == 0 || line[0] != '/' {
		return false
	}
	return len(line) == 1 || line[1] != '/'
}

func Unescape(line string) string {
	return line[1:]
}

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
