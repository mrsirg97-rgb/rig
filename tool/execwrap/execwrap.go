package execwrap

import "os"

const Env = "RIG_EXEC_WRAPPER"

func Args(argv []string) []string {
	if w := os.Getenv(Env); w != "" {
		return append([]string{w, "-exec"}, argv...)
	}
	return argv
}
