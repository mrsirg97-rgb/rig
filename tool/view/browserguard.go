package view

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/v3/pathguard"
	webtool "github.com/mrsirg97-rgb/rig/v3/tool/web"
)

func (b *browser) guard(ctx context.Context, raw string) (string, error) {
	if raw == "about:blank" {
		return raw, nil
	}
	return b.guardRequest(ctx, raw)
}

func (b *browser) guardRequest(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" {
		return "", fmt.Errorf("view: %q is not a URL", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		guarded, err := webtool.GuardURL(ctx, raw, webtool.LookupFn(b.cfg.Lookup))
		if err != nil {
			return "", fmt.Errorf("view: %v", err)
		}
		return guarded, nil
	case "file":
		if u.Host != "" && u.Host != "localhost" {
			return "", fmt.Errorf("view: file:// must name a local path, got host %q", u.Host)
		}
		if u.Path == "" || !filepath.IsAbs(u.Path) {
			return "", fmt.Errorf("view: file:// needs an absolute path")
		}
		abs, err := filepath.Abs(u.Path)
		if err != nil {
			return "", fmt.Errorf("view: %v", err)
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return "", fmt.Errorf("view: %v", err)
		}
		if !pathguard.Under(b.cfg.Cwd, resolved) && !pathguard.Under(b.cfg.RigHome, resolved) {
			return "", fmt.Errorf("view: %q is outside the session's workspace (%s) and the rig home (%s)", resolved, b.cfg.Cwd, b.cfg.RigHome)
		}
		return "file://" + resolved, nil
	case "data", "blob":
		return raw, nil
	default:
		return "", fmt.Errorf("view: %q: only http(s), file:// inside the workspace, data:, or blob: is openable", raw)
	}
}

func (b *browser) binary() (string, error) {
	if b.cfg.Browser == "" {
		return "", errors.New("view: no browser is configured: set RIG_BROWSER or settings.browser to a headless chromium")
	}
	bin := b.cfg.Browser
	if filepath.IsAbs(bin) {
		if fi, err := os.Stat(bin); err != nil || fi.Mode()&0o111 == 0 {
			return "", fmt.Errorf("view: the configured browser %q is not an executable file", bin)
		}
		return bin, nil
	}
	p, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("view: the configured browser %q is not on PATH: %v", bin, err)
	}
	return p, nil
}

func (b *browser) argv(profile, egress string) []string {
	args := []string{
		"--remote-debugging-pipe",
		"--headless",
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--disable-background-networking",
		"--mute-audio",
		fmt.Sprintf("--window-size=%d,%d", viewportW, viewportH),
	}
	args = append(args,
		"--proxy-server="+egress,
		"--proxy-bypass-list=<-loopback>",
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
		"--no-sandbox",
	)
	return args
}

func tailNote(tail *tailBuffer) string {
	s := strings.TrimSpace(tail.String())
	if s == "" {
		return ""
	}
	return "\n[stderr]\n" + s
}
