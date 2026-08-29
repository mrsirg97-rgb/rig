package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

var PluginNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type nameCollisionError struct {
	name string
	file string
}

func (e nameCollisionError) Error() string {
	return fmt.Sprintf("plugins: name collision: %q (%s) is already a native tool", e.name, filepath.Base(e.file))
}

func IsNameCollision(err error) bool {
	var collision nameCollisionError
	return errors.As(err, &collision)
}

func Zone(home, zone string) ([]string, error) {
	dir := filepath.Join(home, "plugins", zone)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".py") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	return files, nil
}

func List(home string) ([]string, error) {
	dir := filepath.Join(home, "plugins")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".py") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	return files, nil
}

func Check(reports []Report, natives map[string]bool) error {
	for _, rep := range reports {
		if rep.Skipped {
			continue
		}
		if natives[rep.Name] {
			return nameCollisionError{name: rep.Name, file: rep.File}
		}
	}
	return nil
}

func Move(dir, name, from, to string) (src, dst string, err error) {
	if name == "" {
		return "", "", fmt.Errorf("no name")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return "", "", fmt.Errorf("%q is not a plugin name (the filename stem)", name)
	}
	src = filepath.Join(dir, from, name+".py")
	dst = filepath.Join(dir, to, name+".py")
	if _, statErr := os.Stat(src); statErr != nil {
		if os.IsNotExist(statErr) {
			return "", "", fmt.Errorf("no plugin %q at %s", name, src)
		}
		return "", "", fmt.Errorf("the move: %v", statErr)
	}
	if info, statErr := os.Lstat(src); statErr != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("%q is a symlink; plugin zone moves require regular files", src)
	}
	if _, statErr := os.Stat(dst); statErr == nil {
		return "", "", fmt.Errorf("%q already exists at %s (remove one)", name, dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", "", fmt.Errorf("the move: %v", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return "", "", fmt.Errorf("the move: %v", err)
	}
	return src, dst, nil
}

func WritePending(home string, natives map[string]bool, name, source string) (path string, created bool, err error) {
	if name == "" {
		return "", false, fmt.Errorf("no name")
	}
	if !PluginNameRe.MatchString(name) {
		return "", false, fmt.Errorf("the name is the filename stem: lowercase, digits and underscores, a leading letter (got %q)", name)
	}
	if natives[name] {
		return "", false, fmt.Errorf("name collision: %q is a native tool", name)
	}
	if strings.TrimSpace(source) == "" {
		return "", false, fmt.Errorf("the source is required")
	}
	for _, want := range []string{"DESCRIPTION", "SCHEMA", "def run("} {
		if !strings.Contains(source, want) {
			return "", false, fmt.Errorf("the plugin contract is a DESCRIPTION, a SCHEMA, and a run(args): missing %s", want)
		}
	}
	dir := filepath.Join(home, "plugins", "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, fmt.Errorf("the write: %v", err)
	}
	path = filepath.Join(dir, name+".py")
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("the write: %s is a symlink", path)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return "", false, fmt.Errorf("the write: %v", statErr)
	}
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		created = true
	}
	if err := os.WriteFile(path, []byte(strings.TrimRight(source, " \t\r\n")+"\n"), 0o644); err != nil {
		return "", false, fmt.Errorf("the write: %v", err)
	}
	return path, created, nil
}

type Ecosystem struct {
	home    string
	Kernel  Kernel
	natives map[string]bool
	swap    func(ctx context.Context, reports []Report) (string, error)
	list    func() (string, error)
}

var _ core.Tool = (*Ecosystem)(nil)

func NewEcosystem(home string, natives map[string]bool, k Kernel, swap func(ctx context.Context, reports []Report) (string, error), list func() (string, error)) *Ecosystem {
	return &Ecosystem{home: home, natives: natives, Kernel: k, swap: swap, list: list}
}

func (e *Ecosystem) Name() string { return "plugins" }

func (e *Ecosystem) Description() string {
	return "the plugin ecosystem: {\"action\": \"list\"|\"create\"|\"delete\"|\"reload\", ...}. list shows the loaded and the skipped; create <name, source> writes a new plugin into plugins/pending/; delete <name> moves a loaded plugin into plugins/disabled/; reload re-runs the discovery over plugins/. Guidelines: a created plugin lands in plugins/pending/ untrusted — the operator installs it with /plugins approve. Reply: the listing, the write/disable, or the discovery's list — loaded, and skipped with reasons."
}

func (e *Ecosystem) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"enum":["list","create","delete","reload"],"description":"the ecosystem verb"},"name":{"type":"string","description":"the plugin name (create/delete)"},"source":{"type":"string","description":"the plugin source (create)"}},"required":["action"]}`)
}

func (e *Ecosystem) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Action string `json:"action"`
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("plugins: bad call (want {action, name, source}): %v", err)
	}
	if in.Action == "" {
		return "", fmt.Errorf("plugins: no action (want {action, name, source})")
	}
	switch in.Action {
	case "list":
		return e.listEcosystem(ctx)
	case "create":
		return e.create(ctx, in.Name, in.Source)
	case "delete":
		return e.delete(ctx, in.Name)
	case "reload":
		return e.reload(ctx)
	default:
		return "", fmt.Errorf("plugins: unknown action %q (want list, create, delete, or reload)", in.Action)
	}
}

func (e *Ecosystem) listEcosystem(ctx context.Context) (string, error) {
	if e.list == nil {
		return "", fmt.Errorf("plugins: list: no listing seam (the root did not wire one)")
	}
	return e.list()
}

func (e *Ecosystem) create(ctx context.Context, name, source string) (string, error) {
	path, created, err := WritePending(e.home, e.natives, name, source)
	if err != nil {
		return "", fmt.Errorf("plugins: create: %v", err)
	}
	verb := "updated"
	if created {
		verb = "created"
	}
	return "plugins: create: " + verb + " " + name + " (" + path + "; the operator installs it with /plugins approve)", nil
}

func (e *Ecosystem) delete(ctx context.Context, name string) (string, error) {
	src, dst, err := Move(filepath.Join(e.home, "plugins"), name, "", "disabled")
	if err != nil {
		return "", fmt.Errorf("plugins: delete: %v", err)
	}
	return "plugins: delete: " + name + " (" + src + " -> " + dst + "; a reload re-registers without it; /plugins enable brings it back)", nil
}

func (e *Ecosystem) reload(ctx context.Context) (string, error) {
	files, err := List(e.home)
	if err != nil {
		return "", fmt.Errorf("plugins: reload: %v", err)
	}
	reports := make([]Report, 0)
	if len(files) > 0 {
		reports, err = DiscoverChecked(ctx, e.Kernel, files, e.natives)
		if err != nil {
			if IsNameCollision(err) {
				return "", err
			}
			return "", fmt.Errorf("plugins: reload: %v", err)
		}
	}
	if err := Check(reports, e.natives); err != nil {
		return "", err
	}
	return e.swap(ctx, reports)
}
