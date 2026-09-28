package rem

import (
	"context"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/core"
)

func attributedSource(explicit *string, ctx context.Context) string {
	if explicit != nil && *explicit != "" {
		return *explicit
	}
	if s, ok := core.SessionFrom(ctx); ok && s != nil && s.ID != "" {
		return s.ID
	}
	return "anon"
}

func scopeOf(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

func scopeCheck(s *string) error {
	if s == nil || *s == "" || *s == "project" || *s == "global" || *s == "all" {
		return nil
	}
	return fmt.Errorf("rem: scope must be project, global, or all, got '%s'", *s)
}

func kindOf(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

func queryOf(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

func recallK(k *int) int {
	if k != nil {
		return *k
	}
	return 0
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
