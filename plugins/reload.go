package plugins

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
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
	if info, statErr := os.Lstat(dst); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		_ = os.Rename(dst, src)
		return "", "", fmt.Errorf("%q is a symlink; plugin zone moves require regular files", dst)
	} else if statErr != nil {
		_ = os.Rename(dst, src)
		return "", "", fmt.Errorf("the move: %v", statErr)
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
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return "", false, fmt.Errorf("the write: %v", err)
	}
	defer f.Close()
	if _, err := f.Write([]byte(strings.TrimRight(source, " \t\r\n") + "\n")); err != nil {
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

func NewEcosystem(home string, natives map[string]bool, k Kernel, swap func(ctx context.Context, reports []Report) (string, error), list func() (string, error)) *Ecosystem {
	return &Ecosystem{home: home, natives: natives, Kernel: k, swap: swap, list: list}
}

func (e *Ecosystem) ListEcosystem(ctx context.Context) (string, error) {
	if e.list == nil {
		return "", fmt.Errorf("plugin: list: no listing seam (the root did not wire one)")
	}
	return e.list()
}

func (e *Ecosystem) Create(ctx context.Context, name, source string) (string, error) {
	path, created, err := WritePending(e.home, e.natives, name, source)
	if err != nil {
		return "", fmt.Errorf("plugin: create: %v", err)
	}
	verb := "updated"
	if created {
		verb = "created"
	}
	return "plugin: create: " + verb + " " + name + " (" + path + "; the operator installs it with /plugins approve)", nil
}

func (e *Ecosystem) Delete(ctx context.Context, name string) (string, error) {
	src, dst, err := Move(filepath.Join(e.home, "plugins"), name, "", "disabled")
	if err != nil {
		return "", fmt.Errorf("plugin: delete: %v", err)
	}
	return "plugin: delete: " + name + " (" + src + " -> " + dst + "; a reload re-registers without it; /plugins enable brings it back)", nil
}

func (e *Ecosystem) Reload(ctx context.Context) (string, error) {
	files, err := List(e.home)
	if err != nil {
		return "", fmt.Errorf("plugin: reload: %v", err)
	}
	reports := make([]Report, 0)
	if len(files) > 0 {
		reports, err = DiscoverChecked(ctx, e.Kernel, files, e.natives)
		if err != nil {
			if IsNameCollision(err) {
				return "", err
			}
			return "", fmt.Errorf("plugin: reload: %v", err)
		}
	}
	if err := Check(reports, e.natives); err != nil {
		return "", err
	}
	return e.swap(ctx, reports)
}
