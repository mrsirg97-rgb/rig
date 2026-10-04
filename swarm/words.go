package swarm

import "strings"

func claimID(reply string) string {
	start := strings.Index(reply, "'")
	if start < 0 {
		return ""
	}
	rest := reply[start+1:]
	end := strings.Index(rest, "'")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
