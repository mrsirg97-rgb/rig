package plugins

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
			return fmt.Errorf("plugins: name collision: %q (%s) is already a native tool", rep.Name, filepath.Base(rep.File))
		}
	}
	return nil
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
	return "the plugin ecosystem, one mutating native: {\"action\": \"list\"|\"create\"|\"delete\"|\"reload\", ...}. list shows the loaded and the skipped; create <name, source> writes a new plugin into plugins/pending/; delete <name> removes a loaded plugin; reload re-runs the discovery over plugins/. Guidelines: a created plugin lands in plugins/pending/ untrusted — the operator installs it with /plugins approve; every verb pauses at the gate. Reply: the listing, the write/remove, or the discovery's list — loaded, and skipped with reasons."
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
		return e.listList(ctx)
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

func (e *Ecosystem) listList(ctx context.Context) (string, error) {
	if e.list == nil {
		return "", fmt.Errorf("plugins: list: no listing seam (the root did not wire one)")
	}
	return e.list()
}

func (e *Ecosystem) create(ctx context.Context, name, source string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("plugins: create: no name")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("plugins: create: %q is not a plugin name (the filename stem)", name)
	}
	if e.natives[name] {
		return "", fmt.Errorf("plugins: create: name collision: %q is already a native tool", name)
	}
	if strings.TrimSpace(source) == "" {
		return "", fmt.Errorf("plugins: create: the source is required")
	}
	dir := filepath.Join(e.home, "plugins", "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("plugins: create: %v", err)
	}
	path := filepath.Join(dir, name+".py")
	if err := os.WriteFile(path, []byte(strings.TrimRight(source, " \t\r\n")+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("plugins: create: %v", err)
	}
	return "plugins: create: wrote " + name + " (" + path + "; the operator installs it with /plugins approve)", nil
}

func (e *Ecosystem) delete(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("plugins: delete: no name")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("plugins: delete: %q is not a plugin name (the filename stem)", name)
	}
	path := filepath.Join(e.home, "plugins", name+".py")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("plugins: delete: no plugin %q at %s", name, path)
		}
		return "", fmt.Errorf("plugins: delete: %v", err)
	}
	return "plugins: delete: removed " + name + " (" + path + "; a reload re-registers without it)", nil
}

func (e *Ecosystem) reload(ctx context.Context) (string, error) {
	files, err := List(e.home)
	if err != nil {
		return "", fmt.Errorf("plugins: reload: %v", err)
	}
	reports := make([]Report, 0)
	if len(files) > 0 {
		reports, err = Discover(ctx, e.Kernel, files)
		if err != nil {
			return "", fmt.Errorf("plugins: reload: %v", err)
		}
	}
	if err := Check(reports, e.natives); err != nil {
		return "", err
	}
	return e.swap(ctx, reports)
}
