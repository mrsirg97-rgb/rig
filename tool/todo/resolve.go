package todo

import (
	"fmt"
	"os"

	"github.com/mrsirg97-rgb/rig/v2/middleware/paths"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

func resolve(raw string) (todostore.Project, error) {
	if raw == "" {
		return todostore.Project{}, fmt.Errorf("todo: scope required: name the workspace this acts on, as a path, or global")
	}
	if raw == scope.Global {
		return todostore.Global(), nil
	}
	dir := paths.Expand(raw)
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return todostore.Project{}, fmt.Errorf("todo: no such project directory: %s", dir)
	}
	return todostore.ProjectOf(dir), nil
}
