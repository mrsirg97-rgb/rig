package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
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
	key     string
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
	if strings.TrimSpace(cfg.key) == "" {
		return fmt.Errorf("update: no verification key: set RIG_UPDATE_KEY or settings.json updateKey to the minisign public key that signs the releases (minisign -G); an unpinned update is refused")
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
	sig, err := fetchBytes(ctx, cfg, releaseURL(cfg, tag, asset+".minisig"))
	if err != nil {
		return fmt.Errorf("update: the release carries no signature for %s (%s.minisig): %v — an unsigned asset is refused", asset, asset, err)
	}
	downloaded, err := os.ReadFile(tmpPath)
	if err != nil {
		return fmt.Errorf("read %s: %v", tmpPath, err)
	}
	if err := verifyMinisign(cfg.key, downloaded, sig); err != nil {
		return fmt.Errorf("update: the release signature refused: %v", err)
	}
	sum2 := sha256.Sum256(downloaded)
	got := hex.EncodeToString(sum2[:])
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

func decodeMinisignLine(text []byte) ([]byte, error) {
	for _, line := range strings.Split(string(text), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "untrusted comment:") {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(line)
		}
		if err != nil {
			return nil, fmt.Errorf("a minisign line is not base64: %v", err)
		}
		return decoded, nil
	}
	return nil, errors.New("no minisign line found")
}

func verifyMinisign(pubText string, data, sigText []byte) error {
	pub, err := decodeMinisignLine([]byte(pubText))
	if err != nil {
		return fmt.Errorf("the pinned key is not a minisign public key: %v", err)
	}
	if len(pub) != 40 {
		return fmt.Errorf("the pinned key decodes to %d bytes, want 40 (8-byte key id + 32-byte ed25519 key)", len(pub))
	}
	sig, err := decodeMinisignLine(sigText)
	if err != nil {
		return fmt.Errorf("the signature is not a minisign signature: %v", err)
	}
	if len(sig) != 72 {
		return fmt.Errorf("the signature decodes to %d bytes, want 72 (64-byte ed25519 signature + 8-byte key id)", len(sig))
	}
	if !bytes.Equal(sig[64:], pub[:8]) {
		return fmt.Errorf("the signature's key id %s does not match the pinned key's %s", hex.EncodeToString(sig[64:]), hex.EncodeToString(pub[:8]))
	}
	if !ed25519.Verify(pub[8:], data, sig[:64]) {
		return errors.New("the signature does not match the asset")
	}
	return nil
}

func fetchBytes(ctx context.Context, cfg updateCfg, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("download %s: %v", url, err)
	}
	resp, err := cfg.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("download %s: %v", url, err)
	}
	return b, nil
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
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset {
			return fields[0], nil
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
