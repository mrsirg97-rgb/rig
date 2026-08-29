package perm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

func Plugins(pluginsDir string) core.ToolMiddleware {
	root, rootErr := resolvedPath(pluginsDir)
	return core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec {
		return func(ctx context.Context, call core.ToolCall) (string, error) {
			if call.Name != "write" && call.Name != "edit" {
				return next(ctx, call)
			}
			if rootErr != nil {
				msg := fmt.Sprintf("permission denied: cannot resolve plugins root %s: %v", pluginsDir, rootErr)
				return msg, errors.New(msg)
			}
			var a struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(call.Args, &a); err != nil || a.Path == "" {
				return next(ctx, call)
			}
			target, resolveErr := resolvedPath(a.Path)
			if resolveErr != nil {
				msg := fmt.Sprintf("permission denied: cannot resolve plugin path %s: %v", a.Path, resolveErr)
				return msg, errors.New(msg)
			}
			rel, err := filepath.Rel(root, target)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return next(ctx, call)
			}
			if rel == "pending" || strings.HasPrefix(rel, "pending"+string(os.PathSeparator)) {
				return next(ctx, call)
			}
			msg := fmt.Sprintf("permission denied: %s is in plugins/ outside plugins/pending/ (plugins install by the operator's /plugins approve; write to plugins/pending/)", target)
			return msg, errors.New(msg)
		}
	})
}

func resolvedPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cur := filepath.Clean(abs)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			parts := append([]string{resolved}, missing...)
			return filepath.Join(parts...), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		missing = append([]string{filepath.Base(cur)}, missing...)
		cur = parent
	}
}
