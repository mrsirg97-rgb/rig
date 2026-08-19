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

// Plugins is the plugin provenance rule (SPEC_SANDBOX 2): the model's
// write and edit refuse a target inside the rig home's plugins/ that
// is not inside plugins/pending/. The model's authoring lands in the
// pending zone (invisible to discovery), and the operator's
// /plugins approve installs — the refusal teaches both. The rule is
// the guard for the honest path, not the boundary: bash can still
// move a file there (the operator's shell is the operator's); the
// worker jail is the boundary, the provenance rule is the workflow.
//
// List it before the Allowlist: first-listed is innermost, so the
// allow-list — the more basic refusal, the tool's name — is
// consulted first, and this rule speaks only for a call the allow-list
// has passed.
func Plugins(pluginsDir string) core.ToolMiddleware {
	root := normalizePath(pluginsDir)
	return core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec {
		return func(ctx context.Context, call core.ToolCall) (string, error) {
			if call.Name != "write" && call.Name != "edit" {
				return next(ctx, call) // the rule names its two tools
			}
			var a struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(call.Args, &a); err != nil || a.Path == "" {
				return next(ctx, call) // the args are the tool's to refuse
			}
			target := normalizePath(a.Path)
			rel, err := filepath.Rel(root, target)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return next(ctx, call) // not the rig home's plugins/: not this rule
			}
			if rel == "pending" || strings.HasPrefix(rel, "pending"+string(os.PathSeparator)) {
				return next(ctx, call) // the forge's landing zone
			}
			msg := fmt.Sprintf("permission denied: %s is in plugins/ outside plugins/pending/ (plugins install by the operator's /plugins approve; write to plugins/pending/)", target)
			return msg, errors.New(msg)
		}
	})
}

// normalizePath canonicalizes the way the file tools do (tool/file's
// normalizePath): absolute and clean, so the rule's path test and the
// tool's agree on the same file.
func normalizePath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}
