// Package diff is the observation diff (SPEC_DIFF): one leaf tool, two
// verbs. files: the working tree against HEAD (or ref), via git diff —
// the shell-out is the files verb's contract (decision 1). last: the
// previous observation of the same tool call (this session only) from
// the state store — a read path over the transcript; the diff itself
// is the pure Go engine (Diff), stdlib only.
package diff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state"
)

// capLines bounds the body of a reply (decision 2): over the cap, the
// first capLines-1 lines are kept, the tail is the loud marker.
const capLines = 100

type tool struct{ db store.DB }

// New hands the state DB to the surface (decision 7): registered at
// the root, once, at the seam; the session comes from ctx.
func New(db store.DB) core.Tool { return tool{db: db} }

func (tool) Name() string { return "diff" }

func (tool) Description() string { return description }

func (tool) Schema() json.RawMessage { return json.RawMessage(schemaJSON) }

const description = "diff a tool call's result against its previous observation, or the working tree against HEAD.\n\n" +
	"mode 'files': the tool shells out to `git diff` and says so: the working tree against HEAD (or ref, optional), optional paths; a non-git cwd refuses loud, naming the reason.\n\n" +
	"mode 'last': the recorded tool calls of this session only (a resumed session is the same session; another session is another world): the newest result of the same call (tool name + exact args; key order and whitespace do not matter, values do) against its n-th previous (n optional, default 1). a read path over state the harness already recorded; nothing new is written.\n\n" +
	"the reply is a unified diff (context 3, ANSI-free, capped, '… K more lines'), or the word 'identical', or 'no earlier observation'.\n\n" +
	"Guidelines: 'did my change actually apply' -> last, with the tool and args of the call that made the change; tree against HEAD -> files; diff of arbitrary strings -> python, not this."

const schemaJSON = `{
  "type": "object",
  "required": ["mode"],
  "properties": {
    "mode": {
      "type": "string",
      "enum": ["files", "last"],
      "description": "files: the working tree against HEAD (git diff); last: the previous observation of the same tool call"
    },
    "ref": {
      "type": "string",
      "description": "files only: the ref to diff against (default HEAD)"
    },
    "paths": {
      "type": "array",
      "items": {"type": "string"},
      "description": "files only: restrict the diff to these paths"
    },
    "tool": {
      "type": "string",
      "description": "last only: the tool name of the observed call"
    },
    "args": {
      "type": "object",
      "description": "last only: the exact args of that call; matched by canonical equality (key order and whitespace do not matter, values do)"
    },
    "n": {
      "type": "integer",
      "minimum": 1,
      "description": "last only: the n-th previous observation (default 1)"
    }
  }
}`

// Exec dispatches the verb and carries the pinned voices (decision 6:
// loud, naming the reason).
func (t tool) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(args, &keys); err != nil {
		return "", fmt.Errorf("diff: args: %v", err)
	}
	known := map[string]bool{"mode": true, "ref": true, "paths": true, "tool": true, "args": true, "n": true}
	for k := range keys {
		if !known[k] {
			return "", fmt.Errorf("diff: unknown argument %q (args, mode, n, paths, ref, tool)", k)
		}
	}
	var mode string
	if raw, ok := keys["mode"]; ok {
		if err := json.Unmarshal(raw, &mode); err != nil {
			return "", errors.New("diff: mode required (files|last)")
		}
	}
	switch mode {
	case "":
		return "", errors.New("diff: mode required (files|last)")
	case "files":
		ref, paths, err := t.filesArgs(keys)
		if err != nil {
			return "", err
		}
		return t.files(ctx, ref, paths)
	case "last":
		name, argsRaw, n, err := t.lastArgs(keys)
		if err != nil {
			return "", err
		}
		return t.last(ctx, name, argsRaw, n)
	default:
		return "", fmt.Errorf("diff: unknown mode %q (files|last)", mode)
	}
}

func (t tool) filesArgs(keys map[string]json.RawMessage) (string, []string, error) {
	var ref string
	if raw, ok := keys["ref"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &ref); err != nil {
			return "", nil, errors.New("diff files: ref must be a string")
		}
	}
	var paths []string
	if raw, ok := keys["paths"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &paths); err != nil {
			return "", nil, errors.New("diff files: paths must be an array of strings")
		}
	}
	return ref, paths, nil
}

func (t tool) lastArgs(keys map[string]json.RawMessage) (string, json.RawMessage, int, error) {
	nameRaw, ok := keys["tool"]
	if !ok {
		return "", nil, 0, errors.New("diff last: tool and args required")
	}
	var name string
	if err := json.Unmarshal(nameRaw, &name); err != nil || name == "" {
		return "", nil, 0, errors.New("diff last: tool and args required")
	}
	argsRaw, ok := keys["args"]
	if !ok || string(argsRaw) == "null" {
		return "", nil, 0, errors.New("diff last: tool and args required")
	}
	n := 1
	if raw, ok := keys["n"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &n); err != nil || n < 1 {
			return "", nil, 0, errors.New("diff last: n must be >= 1")
		}
	}
	return name, argsRaw, n, nil
}

// files shells out to git diff (decision 1): --no-color, -U3, the
// process cwd, the one-dot ref form (ref vs working tree), optional
// paths. The body is capped; a clean tree is the word identical.
func (t tool) files(ctx context.Context, ref string, paths []string) (string, error) {
	cmdArgs := []string{"diff", "--no-color", "-U3"}
	if ref != "" {
		cmdArgs = append(cmdArgs, ref)
	}
	if len(paths) > 0 {
		cmdArgs = append(cmdArgs, "--")
		cmdArgs = append(cmdArgs, paths...)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("diff files: cwd: %v", err)
	}
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		stderr := errb.String()
		if strings.Contains(strings.ToLower(stderr), "not a git repository") {
			return "", fmt.Errorf("diff files: not a git repository (cwd %s)", cwd)
		}
		first := firstLine(stderr)
		if first == "" {
			first = err.Error()
		}
		return "", fmt.Errorf("diff files: %s", first)
	}
	body := strings.TrimSuffix(out.String(), "\n")
	if body == "" {
		return "identical", nil
	}
	return capBody(body), nil
}

// last is the read path (decision 5): the session's rows only, the
// same call matched by canonical args equality at query time, the
// pair diffed by the engine, the header naming both observations.
func (t tool) last(ctx context.Context, name string, argsRaw json.RawMessage, n int) (string, error) {
	sess, ok := core.SessionFrom(ctx)
	if !ok || sess == nil {
		return "", errors.New("diff last: no session in context (the loop threads one)")
	}
	canonical, err := state.CanonicalArgs(string(argsRaw))
	if err != nil {
		return "", errors.New("diff last: tool and args required")
	}
	rows, err := state.RecentToolCalls(ctx, t.db, sess.ID, name, canonical, n)
	if err != nil {
		return "", fmt.Errorf("diff last: %v", err)
	}
	if len(rows) < n+1 {
		return "no earlier observation", nil
	}
	old, fresh := rows[n], rows[0]
	body := Diff(old.Result, fresh.Result,
		old.StartedAt.Format(time.RFC3339Nano), fresh.StartedAt.Format(time.RFC3339Nano))
	if body == "" {
		return "identical", nil
	}
	header := fmt.Sprintf("diff last %s %s · old %s seq %d · new %s seq %d",
		name, canonical, old.StartedAt.Format(time.RFC3339Nano), old.Seq,
		fresh.StartedAt.Format(time.RFC3339Nano), fresh.Seq)
	return header + "\n\n" + capBody(body), nil
}

// firstLine returns the first non-empty trimmed line of s.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// capBody applies the cap (decision 2): over the cap, the first
// capLines-1 lines are kept and the tail is the loud marker.
func capBody(body string) string {
	ln := strings.Split(body, "\n")
	if len(ln) <= capLines {
		return body
	}
	k := len(ln) - (capLines - 1)
	return strings.Join(ln[:capLines-1], "\n") + "\n… " + strconv.Itoa(k) + " more lines"
}
