// The reload (SPEC_PLUGINS 8): the one new primitive — the
// re-discovery over the home's plugins/ and the hand-off to the
// root's swap (the list rebuilds from disk: the same loud skips, the
// same collision refusal, removal free). The listing and the collision
// check are the startup's and the reload's one rule, two doors.

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

// List is the home's plugin listing (SPEC_PLUGINS 2 and 8): the
// plugins/ directory's top-level *.py, in filename order (os.ReadDir
// sorts) — the pending zone, the subdirectories, and the non-.py files
// are not the listing's (the rule the startup's and the zone's listings
// share). No plugins directory, or an empty one, is a no-op that never
// starts the kernel.
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

// Check is the collision's refusal (SPEC_PLUGINS 2's rule at 8's door):
// a loaded plugin named like a native tool — the refusal, the file
// named. A skipped report never collides (it is not a tool). The
// startup's refusal and the reload's are this one rule.
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

// Reload is the plugins_reload tool (SPEC_PLUGINS 8): the re-discovery
// and the swap's hand-off, as a native tool the model can call (the
// operator's /plugins reload and the approve's tail are the same
// action, the root's doors). No arguments: the listing is the home's
// fact, not the model's. The swap takes effect on the next turn, never
// mid-turn (the root's table, the loop's per-turn reads).
type Reload struct {
	home    string
	Kernel  Kernel
	natives map[string]bool
	swap    func(ctx context.Context, reports []Report) (string, error)
}

var _ core.Tool = (*Reload)(nil) // the seam is compile-time enforced

// NewReload is the plugins_reload tool: the home's listing, the shared
// kernel (the discovery's and the call's — the namespace the forge and
// the python tool share, 3's cost), the native set (the collision
// check's refusal set), and the root's swap (the rebuild).
func NewReload(home string, natives map[string]bool, k Kernel, swap func(ctx context.Context, reports []Report) (string, error)) *Reload {
	return &Reload{home: home, natives: natives, Kernel: k, swap: swap}
}

// Name is the native's: plugins_reload.
func (r *Reload) Name() string { return "plugins_reload" }

// Description is the wire's: the re-discovery and the next-turn effect,
// and the shared namespace (the python tool's import is the reload's).
func (r *Reload) Description() string {
	return "re-run the python plugin discovery over the rig home's plugins/ directory: the same loud skips, the same collision refusal, and removal free (the list rebuilds from disk). A new or changed plugin is registered for the next turn, and its functions are importable from the python tool right away (the shared kernel). No arguments."
}

// Schema is the empty object: no arguments.
func (r *Reload) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

// Exec is the reload's action (SPEC_PLUGINS 8): the listing (no files,
// no kernel), the discovery through the shared kernel (the loud skips
// in the reply), the collision check (the refusal before the swap),
// and the hand-off to the root's swap — its reply verbatim. A discovery
// failure is the kernel's reason under the reload's prefix; the table
// — and the wire — are untouched (the swap never ran).
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
