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

type Reload struct {
	home    string
	Kernel  Kernel
	natives map[string]bool
	swap    func(ctx context.Context, reports []Report) (string, error)
}

var _ core.Tool = (*Reload)(nil)

func NewReload(home string, natives map[string]bool, k Kernel, swap func(ctx context.Context, reports []Report) (string, error)) *Reload {
	return &Reload{home: home, natives: natives, Kernel: k, swap: swap}
}

func (r *Reload) Name() string { return "plugins_reload" }

func (r *Reload) Description() string {
	return "re-run the python plugin discovery over the rig home's plugins/ directory: the same loud skips, the same collision refusal, and removal free (the list rebuilds from disk). A new or changed plugin is registered for the next turn, and its functions are importable from the python tool right away (the shared kernel). No arguments."
}

func (r *Reload) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (r *Reload) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	files, err := List(r.home)
	if err != nil {
		return "", fmt.Errorf("plugins: reload: %v", err)
	}
	reports := make([]Report, 0)
	if len(files) > 0 {
		reports, err = Discover(ctx, r.Kernel, files)
		if err != nil {
			return "", fmt.Errorf("plugins: reload: %v", err)
		}
	}
	if err := Check(reports, r.natives); err != nil {
		return "", err
	}
	return r.swap(ctx, reports)
}
