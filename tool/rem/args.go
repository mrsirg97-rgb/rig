package rem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/middleware/paths"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
)

func attributedSource(explicit string, ctx context.Context) string {
	if explicit != "" {
		return explicit
	}
	if s, ok := core.SessionFrom(ctx); ok && s != nil && s.ID != "" {
		return s.ID
	}
	return "anon"
}

func scopeOf(s string) (string, bool, error) {
	if s == "" {
		return "", false, fmt.Errorf("rem: scope required: name the workspace this acts on, as a path, or global")
	}
	if s == scope.Global {
		return "", true, nil
	}
	dir, err := filepath.Abs(paths.Expand(s))
	if err != nil {
		return "", false, fmt.Errorf("rem: scope %q: %v", s, err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", false, fmt.Errorf("rem: no such project directory: %s", s)
	}
	return dir, false, nil
}

func internalScope(global bool) string {
	if global {
		return scope.Global
	}
	return ""
}

func importanceOf(v *float64) (float64, bool, error) {
	if v == nil {
		return 0.5, false, nil
	}
	if *v < 0 || *v > 1 {
		return 0, false, fmt.Errorf("rem: importance must be within 0..1, got %g", *v)
	}
	return *v, true, nil
}

func supersedesOf(v any) ([]int64, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case float64:
		if t != float64(int64(t)) || t < 1 {
			return nil, fmt.Errorf("rem: supersedes must be memory ids of at least 1, got %g", t)
		}
		return []int64{int64(t)}, nil
	case []any:
		var out []int64
		for _, e := range t {
			f, ok := e.(float64)
			if !ok || f != float64(int64(f)) || f < 1 {
				return nil, fmt.Errorf("rem: supersedes must be memory ids of at least 1")
			}
			out = append(out, int64(f))
		}
		return out, nil
	}
	return nil, fmt.Errorf("rem: supersedes must be a memory id or a list of ids")
}

func idsOf(v []any) ([]int64, error) {
	if v == nil {
		return nil, nil
	}
	var out []int64
	for _, e := range v {
		f, ok := e.(float64)
		if !ok || f != float64(int64(f)) || f < 1 {
			return nil, fmt.Errorf("rem: ids must be memory ids of at least 1")
		}
		out = append(out, int64(f))
	}
	return out, nil
}
