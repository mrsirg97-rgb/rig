// The /plugins command (SPEC_PLUGINS 4, SPEC_SANDBOX 2): the loaded
// plugins (name, description, file) and the skipped ones with their
// reasons; the provenance verbs — pending lists the forge's landing
// zone with each file's DESCRIPTION, approve <name> installs one (the
// operator's verb, Frontend-side by construction: it never runs from a
// tool call). The rows and the zone cross as plain Env fields
// (command stays a leaf over core and models).
package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PluginInfo is one discovered plugin file (SPEC_PLUGINS 4): the name
// (the filename stem), the description and file when loaded, the
// reason when skipped.
type PluginInfo struct {
	Name        string
	Description string
	File        string
	Skipped     bool
	Reason      string
}

type pluginsCmd struct{}

func (pluginsCmd) Name() string { return "plugins" }

func (pluginsCmd) Description() string {
	return "list the python plugins: the loaded ones, the skipped ones, and the pending zone; approve <name> installs a pending plugin"
}

// Sub is the TUI's argument hints (SPEC_TUI 9, amended): the
// provenance verbs (SPEC_SANDBOX 2).
func (pluginsCmd) Sub() []Sub {
	return []Sub{
		{Name: "pending", Desc: "list the pending zone (the model's authoring), with each file's DESCRIPTION"},
		{Name: "approve", Desc: "approve <name>: move the pending plugin to the top level (the operator's verb)"},
	}
}

const usage = "plugins: usage: plugins | plugins pending | plugins approve <name>"

func (pluginsCmd) Run(ctx context.Context, args string, env any) (string, error) {
	fields := strings.Fields(args)
	switch {
	case len(fields) == 0:
		return listPlugins(env)
	case len(fields) == 1 && fields[0] == "pending":
		return pendingList(env)
	case len(fields) == 2 && fields[0] == "approve":
		return approve(env, fields[1])
	default:
		return "", errors.New(usage)
	}
}

// listPlugins is the listing (SPEC_PLUGINS 4), unchanged by the
// provenance rule: the discovery's rows, in file order.
func listPlugins(env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if e.Plugins == nil {
		return "", errors.New("plugins: no plugins seam (the root did not wire one)")
	}
	if len(e.Plugins) == 0 {
		return "plugins: none", nil
	}
	loaded, skipped := 0, 0
	for _, p := range e.Plugins {
		if p.Skipped {
			skipped++
		} else {
			loaded++
		}
	}
	var b []byte
	b = fmt.Appendf(b, "plugins: %d loaded, %d skipped\n", loaded, skipped)
	if loaded > 0 {
		b = append(b, "loaded:\n"...)
		for _, p := range e.Plugins {
			if !p.Skipped {
				b = fmt.Appendf(b, "  %s: %s (%s)\n", p.Name, p.Description, p.File)
			}
		}
	}
	if skipped > 0 {
		b = append(b, "skipped:\n"...)
		for _, p := range e.Plugins {
			if p.Skipped {
				b = fmt.Appendf(b, "  %s: %s\n", filepath.Base(p.File), p.Reason)
			}
		}
	}
	return string(b), nil
}

// pendingList is the zone's listing (SPEC_SANDBOX 2): each pending
// file's name and DESCRIPTION, so the operator reads before blessing.
// The DESCRIPTION is the file's top-level string literal, read without
// running the file — a pending file is untrusted, and reading is not
// the moment to execute it. A file without one is named as absent.
func pendingList(env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if e.PluginsDir == "" {
		return "", errors.New("plugins: no plugins seam (the root did not wire one)")
	}
	zone := filepath.Join(e.PluginsDir, "pending")
	entries, err := os.ReadDir(zone)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "plugins: no pending plugins", nil
		}
		return "", fmt.Errorf("plugins: pending: %v", err)
	}
	var rows []string
	for _, en := range entries { // ReadDir sorts: filename order, as the listing
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".py") {
			continue // the zone's files are its top-level *.py, as the top level's
		}
		name := strings.TrimSuffix(en.Name(), ".py")
		path := filepath.Join(zone, en.Name())
		rows = append(rows, fmt.Sprintf("  %s: %s (%s)", name, descriptionOf(path), path))
	}
	if len(rows) == 0 {
		return "plugins: no pending plugins", nil
	}
	return fmt.Sprintf("plugins: %d pending\n%s\n", len(rows), strings.Join(rows, "\n")), nil
}

// descriptionRe is the file's DESCRIPTION (SPEC_PLUGINS 2's contract):
// the top-level assignment to a quoted string literal, on one line.
// One pattern per quote type (RE2 has neither lookahead nor
// backreferences): the content is escaped pairs or unescaped
// characters of the other type, closed by the same quote.
var (
	descriptionDouble = regexp.MustCompile(`(?m)^[ \t]*DESCRIPTION[ \t]*=[ \t]*"((?:\\.|[^\\"])*?)"`)
	descriptionSingle = regexp.MustCompile(`(?m)^[ \t]*DESCRIPTION[ \t]*=[ \t]*'((?:\\.|[^\\'])*)?'`)
)

// descriptionOf is the file's DESCRIPTION, or the named absence. The
// literal's two common escapes are undone for the line.
func descriptionOf(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("(read: %v)", err)
	}
	for _, re := range []*regexp.Regexp{descriptionDouble, descriptionSingle} {
		if m := re.FindSubmatch(data); m != nil && m[1] != nil && string(m[1]) != "" {
			return strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\'`, "'").Replace(string(m[1]))
		}
	}
	return "(no DESCRIPTION)"
}

// approve is the operator's promotion (SPEC_SANDBOX 2): move
// pending/<name>.py to the top level. The checks, in order: the
// pending file exists (the zone's file), the name is not a native tool
// (the existing collision rule at the new door, its voice), and the
// top level has no file of the name (a clobber is not an operator's
// verb by accident). The move is the atomic rename; the discovery
// loads the result at the next start — the reload is SPEC_PLUGINS 8.
func approve(env any, name string) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if e.PluginsDir == "" {
		return "", errors.New("plugins: no plugins seam (the root did not wire one)")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("plugins: approve: %q is not a plugin name (the filename stem)", name)
	}
	src := filepath.Join(e.PluginsDir, "pending", name+".py")
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("plugins: approve: no pending plugin %q (%s absent)", name, src)
		}
		return "", fmt.Errorf("plugins: approve: %v", err)
	}
	if e.Tools != nil {
		if _, collides := e.Tools[name]; collides {
			return "", fmt.Errorf("plugins: name collision: %q (%s.py) is already a native tool", name, name)
		}
	}
	dst := filepath.Join(e.PluginsDir, name+".py")
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("plugins: approve: %q is already installed (%s exists — remove it to install the pending one)", name, dst)
	}
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("plugins: approve: %v", err)
	}
	return fmt.Sprintf("plugins: approved %s (%s -> %s; the discovery loads it at the next start)", name, src, dst), nil
}
