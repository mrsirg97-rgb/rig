package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newUpdateSrv(t *testing.T, latest string, asset []byte, assetSum string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/"+updateRepo+"/releases/latest":
			http.Redirect(w, r, "/"+updateRepo+"/releases/tag/v"+latest, http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/"+updateRepo+"/releases/tag/"):
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/"+updateRepo+"/releases/download/v"+latest+"/checksums.txt"):
			fmt.Fprintf(w, "%s  rig_linux_amd64\n%s  rig_darwin_amd64\n", assetSum, strings.Repeat("0", 64))
		case strings.HasPrefix(r.URL.Path, "/"+updateRepo+"/releases/download/v"+latest+"/rig_linux_amd64"):
			_, _ = w.Write(asset)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func updateCfgFor(t *testing.T, srv *httptest.Server, version, bin string) updateCfg {
	t.Helper()
	return updateCfg{
		base:    srv.URL,
		repo:    updateRepo,
		version: version,
		bin:     bin,
		goos:    "linux",
		arch:    "amd64",
		out:     &bytes.Buffer{},
	}
}

func writeOld(t *testing.T, bin string) {
	t.Helper()
	if err := os.WriteFile(bin, []byte("old binary"), 0o755); err != nil {
		t.Fatalf("write old binary: %v", err)
	}
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestUpdateReplacesInPlace(t *testing.T) {
	asset := []byte("new binary bytes")
	srv := newUpdateSrv(t, "0.15.1", asset, sha256Hex(asset))
	dir := t.TempDir()
	bin := filepath.Join(dir, "rig")
	writeOld(t, bin)
	cfg := updateCfgFor(t, srv, "0.15.0", bin)
	if err := update(context.Background(), cfg); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("read new binary: %v", err)
	}
	if string(got) != string(asset) {
		t.Fatalf("binary = %q, want %q", got, asset)
	}
	fi, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("stat new binary: %v", err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", fi.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "rig" {
		t.Fatalf("dir has %d entries (want only rig), old file gone", len(entries))
	}
	out := cfg.out.(*bytes.Buffer).String()
	if !strings.Contains(out, "rig: 0.15.0 -> 0.15.1 ("+bin+")") {
		t.Fatalf("output %q missing the version->version line", out)
	}
	if !strings.Contains(out, "running sessions keep the old binary") {
		t.Fatalf("output %q missing the running-sessions note", out)
	}
}

func TestUpdateBadChecksumRefuses(t *testing.T) {
	asset := []byte("new binary bytes")
	srv := newUpdateSrv(t, "0.15.1", asset, strings.Repeat("f", 64))
	dir := t.TempDir()
	bin := filepath.Join(dir, "rig")
	writeOld(t, bin)
	cfg := updateCfgFor(t, srv, "0.15.0", bin)
	err := update(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("update err = %v, want a checksum mismatch", err)
	}
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if string(got) != "old binary" {
		t.Fatalf("binary = %q, want the old bytes (nothing written)", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "rig" {
		t.Fatalf("dir has %d entries (want only rig), temp file gone", len(entries))
	}
}

func TestUpdateAlreadyLatestIsNoOp(t *testing.T) {
	asset := []byte("new binary bytes")
	srv := newUpdateSrv(t, "0.15.0", asset, sha256Hex(asset))
	dir := t.TempDir()
	bin := filepath.Join(dir, "rig")
	writeOld(t, bin)
	cfg := updateCfgFor(t, srv, "0.15.0", bin)
	if err := update(context.Background(), cfg); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if string(got) != "old binary" {
		t.Fatalf("binary = %q, want unchanged", got)
	}
	out := cfg.out.(*bytes.Buffer).String()
	if !strings.Contains(out, "rig: already at 0.15.0 (latest)") {
		t.Fatalf("output %q missing the already-latest line", out)
	}
}

func TestUpdateUnwritableDirNamesTheFix(t *testing.T) {
	asset := []byte("new binary bytes")
	srv := newUpdateSrv(t, "0.15.1", asset, sha256Hex(asset))
	dir := t.TempDir()
	bin := filepath.Join(dir, "rig")
	writeOld(t, bin)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	cfg := updateCfgFor(t, srv, "0.15.0", bin)
	err := update(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "sudo rig -update") {
		t.Fatalf("update err = %v, want the dir named and the sudo line", err)
	}
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if string(got) != "old binary" {
		t.Fatalf("binary = %q, want the old bytes", got)
	}
}

func TestUpdateNoAssetForPlatform(t *testing.T) {
	asset := []byte("new binary bytes")
	srv := newUpdateSrv(t, "0.15.1", asset, sha256Hex(asset))
	dir := t.TempDir()
	cfg := updateCfgFor(t, srv, "0.15.0", filepath.Join(dir, "rig"))
	cfg.goos = "windows"
	cfg.arch = "amd64"
	err := update(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "no release asset for windows/amd64") {
		t.Fatalf("update err = %v, want the no-asset refusal", err)
	}
}

func TestUpdateAheadOfLatestSaysSo(t *testing.T) {
	asset := []byte("new binary bytes")
	srv := newUpdateSrv(t, "0.15.0", asset, sha256Hex(asset))
	dir := t.TempDir()
	cfg := updateCfgFor(t, srv, "0.15.1", filepath.Join(dir, "rig"))
	err := update(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "older than this build") || !strings.Contains(err.Error(), "not published as a tag") {
		t.Fatalf("update err = %v, want the ahead-of-latest refusal", err)
	}
}
