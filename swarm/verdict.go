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

type verdict struct {
	kind   string
	reason string
}

func parseVerdict(stdout string) verdict {
	lines := strings.Split(stdout, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "verdict:") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "verdict:"))
		if rest == "accept" {
			return verdict{kind: "accept"}
		}
		if strings.HasPrefix(rest, "reject") {
			reason := strings.TrimSpace(strings.TrimPrefix(rest, "reject"))
			if reason == "" {
				return verdict{}
			}
			if len(reason) > maxVerdictReason {
				reason = reason[:maxVerdictReason]
			}
			return verdict{kind: "reject", reason: reason}
		}
		return verdict{}
	}
	return verdict{}
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
