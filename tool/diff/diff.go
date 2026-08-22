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

const capLines = 100

type tool struct{ db store.DB }

func New(db store.DB) core.Tool { return tool{db: db} }

func (tool) Name() string { return "diff" }

func (tool) Description() string { return description }

func (tool) Schema() json.RawMessage { return json.RawMessage(schemaJSON) }

const description = "diff the working tree against HEAD (mode files, git diff), or a tool call's newest result against " +
	"its previous observation in this session (mode last: the same tool and args). Guidelines: 'did my change " +
	"actually apply' -> last with that call's tool and args; the tree -> files; arbitrary strings -> python, not this. " +
	"Reply: a unified diff (context 3, capped), 'identical', or 'no earlier observation'; a non-git cwd refuses files by name."

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

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func capBody(body string) string {
	ln := strings.Split(body, "\n")
	if len(ln) <= capLines {
		return body
	}
	k := len(ln) - (capLines - 1)
	return strings.Join(ln[:capLines-1], "\n") + "\n… " + strconv.Itoa(k) + " more lines"
}
