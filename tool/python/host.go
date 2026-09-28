package python

import (
	"os"
	"path/filepath"
)

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}

func defaultInterpreter() string {
	return filepath.Join(homeDir(), ".pi", "agent", "kernel-venv", "bin", "python")
}

func rigHome() string {
	if v := os.Getenv("RIG_HOME"); v != "" {
		return v
	}
	return filepath.Join(homeDir(), ".rig")
}

func DefaultHost() string {
	local := filepath.Join(homeDir(), ".pi", "agent", "kernel", "kernel_host.py")
	if _, err := os.Stat(local); err == nil {
		return local
	}
	dir := filepath.Join(rigHome(), "kernel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, "kernel_host.py")
	if existing, err := os.ReadFile(path); err != nil || string(existing) != kernelHostSrc {
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(kernelHostSrc), 0o644); err == nil {
			os.Rename(tmp, path)
		}
	}
	return path
}
