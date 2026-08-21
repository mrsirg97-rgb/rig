package rem

import "path/filepath"

// FilePath resolves the rem store the way the root does (SPEC_SERVE 2):
// one file under <home>/rem, cwd-scoped by a column inside (SPEC_STATE).
// The root and the dashboard share this one source.
func FilePath(home string) string {
	return filepath.Join(home, "rem", "rem.sqlite")
}
