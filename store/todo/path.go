package todo

import "path/filepath"

func FilePath(home string) string {
	return filepath.Join(home, "todo", "todo.sqlite")
}
