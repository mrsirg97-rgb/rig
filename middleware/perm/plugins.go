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
	"github.com/mrsirg97-rgb/rig/middleware/paths"
)

const provenanceVoice = "plugins install by the operator's /plugins approve; write to plugins/pending/"

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
			lex, lexErr := lexicalPath(a.Path)
			if lexErr != nil {
				msg := fmt.Sprintf("permission denied: cannot resolve plugin path %s: %v", a.Path, lexErr)
				return msg, errors.New(msg)
			}
			targetRel, targetIn := zoneRel(root, target)
			lexRel, lexIn := zoneRel(root, lex)
			if targetIn && inPending(targetRel) {
				return next(ctx, call)
			}
			if !targetIn && !lexIn {
				return next(ctx, call)
			}
			if lexIn && inPending(lexRel) {
				pending, pendingErr := resolvedPath(filepath.Join(root, "pending"))
				if pendingErr == nil {
					if _, within := zoneRel(pending, target); within {
						return next(ctx, call)
					}
				}
			}
			if targetIn {
				msg := fmt.Sprintf("permission denied: %s is in plugins/ outside plugins/pending/ (%s)", target, provenanceVoice)
				return msg, errors.New(msg)
			}
			msg := fmt.Sprintf("permission denied: %s resolves outside the plugins root (%s) (%s)", lex, target, provenanceVoice)
			return msg, errors.New(msg)
		}
	})
}

func zoneRel(root, path string) (string, bool) {
	if root == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return rel, false
	}
	return rel, true
}

func inPending(rel string) bool {
	return rel == "pending" || strings.HasPrefix(rel, "pending"+string(os.PathSeparator))
}

func lexicalPath(path string) (string, error) {
	abs, err := filepath.Abs(paths.Expand(path))
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func resolvedPath(path string) (string, error) {
	cur, err := lexicalPath(path)
	if err != nil {
		return "", err
	}
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
