package delegate

import (
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

var doingSet = []string{"bash", "read", "write", "edit", "view", "python", "web", "rem"}

func doingAllow(allow []string) []string {
	if len(allow) == 0 {
		return []string{sched.NoToolsAllow}
	}
	var out []string
	for _, name := range allow {
		for _, d := range doingSet {
			if name == d {
				out = append(out, name)
				break
			}
		}
	}
	if len(out) == 0 {
		return []string{sched.NoToolsAllow}
	}
	return out
}
