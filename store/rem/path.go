package rem

import "path/filepath"

func FilePath(home string) string {
	return filepath.Join(home, "rem", "rem.sqlite")
}
