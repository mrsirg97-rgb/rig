package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	githubBase = "https://github.com"
	updateRepo = "mrsirg97-rgb/rig"
)

type updateCfg struct {
	base    string
	repo    string
	version string
	bin     string
	goos    string
	arch    string
	out     io.Writer
	client  *http.Client
}

func defaultUpdateCfg() (updateCfg, error) {
	exe, err := os.Executable()
	if err != nil {
		return updateCfg{}, fmt.Errorf("resolve the running binary: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return updateCfg{
		base:    githubBase,
		repo:    updateRepo,
		version: Version,
		bin:     exe,
		goos:    runtime.GOOS,
		arch:    runtime.GOARCH,
		out:     os.Stdout,
		client:  http.DefaultClient,
	}, nil
}

func update(ctx context.Context, cfg updateCfg) error {
	if cfg.bin == "" {
		return fmt.Errorf("update: no binary path to replace")
	}
	if cfg.out == nil {
		cfg.out = os.Stdout
	}
	if cfg.client == nil {
		cfg.client = http.DefaultClient
	}
	tag, err := latestTag(ctx, cfg)
	if err != nil {
		return err
	}
	latest := strings.TrimPrefix(tag, "v")
	cmp, err := compareVersions(latest, cfg.version)
	if err != nil {
		return err
	}
	switch {
	case cmp < 0:
		return fmt.Errorf("the latest release is %s, older than this build (%s); %s is not published as a tag — no downgrade", latest, cfg.version, cfg.version)
	case cmp == 0:
		fmt.Fprintf(cfg.out, "rig: already at %s (latest)\n", cfg.version)
		return nil
	}
	asset, err := assetName(cfg.goos, cfg.arch)
	if err != nil {
		return err
	}
	sum, err := fetchChecksum(ctx, cfg, tag, asset)
	if err != nil {
		return err
	}
	dir := filepath.Dir(cfg.bin)
	tmp, err := os.CreateTemp(dir, "rig-*.tmp")
	if err != nil {
		return fmt.Errorf("cannot write %s: %v — grant write access to %s and retry (e.g. sudo rig -update)", dir, err, dir)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := download(ctx, cfg, releaseURL(cfg, tag, asset), tmp); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %v", tmpPath, err)
	}
	got, err := sha256File(tmpPath)
	if err != nil {
		return fmt.Errorf("checksum %s: %v", tmpPath, err)
	}
	if got != sum {
		return fmt.Errorf("checksum mismatch for %s (%s != %s)", asset, got, sum)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("chmod %s: %v", tmpPath, err)
	}
	if err := os.Rename(tmpPath, cfg.bin); err != nil {
		return fmt.Errorf("replace %s: %v", cfg.bin, err)
	}
	fmt.Fprintf(cfg.out, "rig: %s -> %s (%s)\n", cfg.version, latest, cfg.bin)
	fmt.Fprintf(cfg.out, "running sessions keep the old binary until restarted; the scheduler's next fire gets the new one\n")
	return nil
}

func latestTag(ctx context.Context, cfg updateCfg) (string, error) {
	url := cfg.base + "/" + cfg.repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("resolve the latest release: %v", err)
	}
	resp, err := cfg.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve the latest release: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("resolve the latest release: status %d", resp.StatusCode)
	}
	path := resp.Request.URL.Path
	tag := path[strings.LastIndex(path, "/")+1:]
	if !strings.HasPrefix(tag, "v") {
		return "", fmt.Errorf("resolve the latest release: %q is not a tag", tag)
	}
	return tag, nil
}

func assetName(goos, goarch string) (string, error) {
	if goos != "linux" && goos != "darwin" {
		return "", fmt.Errorf("no release asset for %s/%s (linux/darwin only)", goos, goarch)
	}
	if goarch != "amd64" && goarch != "arm64" {
		return "", fmt.Errorf("no release asset for %s/%s (amd64/arm64 only)", goos, goarch)
	}
	return "rig_" + goos + "_" + goarch, nil
}

func fetchChecksum(ctx context.Context, cfg updateCfg, tag, asset string) (string, error) {
	url := releaseURL(cfg, tag, "checksums.txt")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("download %s: %v", url, err)
	}
	resp, err := cfg.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("download %s: %v", url, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, asset) {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				return fields[0], nil
			}
		}
	}
	return "", fmt.Errorf("checksums.txt has no line for %s", asset)
}

func download(ctx context.Context, cfg updateCfg, url string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("download %s: %v", url, err)
	}
	resp, err := cfg.client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	if _, err := io.Copy(w, io.LimitReader(resp.Body, 1<<30)); err != nil {
		return fmt.Errorf("download %s: %v", url, err)
	}
	return nil
}

func releaseURL(cfg updateCfg, tag, name string) string {
	return cfg.base + "/" + cfg.repo + "/releases/download/" + tag + "/" + name
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func parseVersion(v string) ([]int, error) {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("%q is not a semver", v)
		}
		nums[i] = n
	}
	return nums, nil
}

func compareVersions(a, b string) (int, error) {
	va, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	vb, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < len(va) && i < len(vb); i++ {
		if va[i] != vb[i] {
			if va[i] < vb[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	switch {
	case len(va) < len(vb):
		return -1, nil
	case len(va) > len(vb):
		return 1, nil
	}
	return 0, nil
}
