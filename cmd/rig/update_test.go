package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/blake2b"
)

type minisignTestKey struct {
	pubText string
	sign    func([]byte) []byte
}

// newMinisignKey builds the wire format minisign 0.11 actually produces:
// the public key is 2-byte "Ed" + 8-byte key id + 32-byte ed25519 key, and
// the signature is 2-byte "ED" + 8-byte key id + 64-byte ed25519 signature
// over the BLAKE2b-512 digest of the file (minisign's default hashed mode).
func newMinisignKey(t *testing.T) minisignTestKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keyID := pub[:8]
	blob := append([]byte("Ed"), keyID...)
	blob = append(blob, pub...)
	pubText := "untrusted comment: minisign public key\n" +
		base64.StdEncoding.EncodeToString(blob)
	return minisignTestKey{pubText: pubText, sign: func(data []byte) []byte {
		h := blake2b.Sum512(data)
		sig := ed25519.Sign(priv, h[:])
		sigBlob := append([]byte("ED"), keyID...)
		sigBlob = append(sigBlob, sig...)
		return []byte("untrusted comment: signature from minisign secret key\n" +
			base64.StdEncoding.EncodeToString(sigBlob))
	}}
}

func newUpdateSrv(t *testing.T, latest string, asset []byte, checksums string, sig []byte) *httptest.Server {
	t.Helper()
	assetPath := "/" + updateRepo + "/releases/download/v" + latest + "/rig_linux_amd64"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/"+updateRepo+"/releases/latest":
			http.Redirect(w, r, "/"+updateRepo+"/releases/tag/v"+latest, http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/"+updateRepo+"/releases/tag/"):
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/"+updateRepo+"/releases/download/v"+latest+"/checksums.txt":
			fmt.Fprint(w, checksums)
		case r.URL.Path == assetPath:
			_, _ = w.Write(asset)
		case r.URL.Path == assetPath+".minisig":
			if sig == nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(sig)
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

func checksumsFor(sum string) string {
	return fmt.Sprintf("%s  rig_linux_amd64\n%s  rig_darwin_amd64\n", sum, strings.Repeat("0", 64))
}

func TestUpdateReplacesInPlace(t *testing.T) {
	key := newMinisignKey(t)
	asset := []byte("new binary bytes")
	srv := newUpdateSrv(t, "0.15.1", asset, checksumsFor(sha256Hex(asset)), key.sign(asset))
	dir := t.TempDir()
	bin := filepath.Join(dir, "rig")
	writeOld(t, bin)
	cfg := updateCfgFor(t, srv, "0.15.0", bin)
	cfg.key = key.pubText
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
	key := newMinisignKey(t)
	asset := []byte("new binary bytes")
	srv := newUpdateSrv(t, "0.15.1", asset, checksumsFor(strings.Repeat("f", 64)), key.sign(asset))
	dir := t.TempDir()
	bin := filepath.Join(dir, "rig")
	writeOld(t, bin)
	cfg := updateCfgFor(t, srv, "0.15.0", bin)
	cfg.key = key.pubText
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
	srv := newUpdateSrv(t, "0.15.0", asset, checksumsFor(sha256Hex(asset)), nil)
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
	key := newMinisignKey(t)
	asset := []byte("new binary bytes")
	srv := newUpdateSrv(t, "0.15.1", asset, checksumsFor(sha256Hex(asset)), key.sign(asset))
	dir := t.TempDir()
	bin := filepath.Join(dir, "rig")
	writeOld(t, bin)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	cfg := updateCfgFor(t, srv, "0.15.0", bin)
	cfg.key = key.pubText
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
	srv := newUpdateSrv(t, "0.15.1", asset, checksumsFor(sha256Hex(asset)), nil)
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
	srv := newUpdateSrv(t, "0.15.0", asset, checksumsFor(sha256Hex(asset)), nil)
	dir := t.TempDir()
	cfg := updateCfgFor(t, srv, "0.15.1", filepath.Join(dir, "rig"))
	err := update(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "older than this build") || !strings.Contains(err.Error(), "not published as a tag") {
		t.Fatalf("update err = %v, want the ahead-of-latest refusal", err)
	}
}

func TestDefaultUpdateCfgNamesTheRunningBinary(t *testing.T) {
	cfg, err := defaultUpdateCfg()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.bin == "" || !filepath.IsAbs(cfg.bin) {
		t.Fatalf("bin = %q, want the running binary's absolute path", cfg.bin)
	}
	if _, err := os.Stat(cfg.bin); err != nil {
		t.Fatalf("bin must exist: %v", err)
	}
}

func TestUpdateWithNoBinRefusesBeforeAnyRequest(t *testing.T) {
	err := update(context.Background(), updateCfg{version: "0.1.0"})
	if err == nil || !strings.Contains(err.Error(), "no binary path") {
		t.Fatalf("an empty bin must refuse by name, got %v", err)
	}
}

func TestUpdateRefusesWithoutAVerificationKey(t *testing.T) {
	key := newMinisignKey(t)
	asset := []byte("new binary bytes")
	srv := newUpdateSrv(t, "0.15.1", asset, checksumsFor(sha256Hex(asset)), key.sign(asset))
	dir := t.TempDir()
	bin := filepath.Join(dir, "rig")
	writeOld(t, bin)
	cfg := updateCfgFor(t, srv, "0.15.0", bin)
	err := update(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "verification key") {
		t.Fatalf("update err = %v, want the missing-key refusal", err)
	}
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if string(got) != "old binary" {
		t.Fatalf("binary = %q, want the old bytes (nothing written)", got)
	}
}

func TestUpdateRefusesAnUnsignedRelease(t *testing.T) {
	key := newMinisignKey(t)
	asset := []byte("new binary bytes")
	srv := newUpdateSrv(t, "0.15.1", asset, checksumsFor(sha256Hex(asset)), nil)
	dir := t.TempDir()
	bin := filepath.Join(dir, "rig")
	writeOld(t, bin)
	cfg := updateCfgFor(t, srv, "0.15.0", bin)
	cfg.key = key.pubText
	err := update(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "no signature") {
		t.Fatalf("update err = %v, want the unsigned-release refusal", err)
	}
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if string(got) != "old binary" {
		t.Fatalf("binary = %q, want the old bytes (nothing written)", got)
	}
}

func TestUpdateRefusesATamperedAsset(t *testing.T) {
	key := newMinisignKey(t)
	asset := []byte("new binary bytes")
	tampered := []byte("new binary bytes and more")
	srv := newUpdateSrv(t, "0.15.1", tampered, checksumsFor(sha256Hex(tampered)), key.sign(asset))
	dir := t.TempDir()
	bin := filepath.Join(dir, "rig")
	writeOld(t, bin)
	cfg := updateCfgFor(t, srv, "0.15.0", bin)
	cfg.key = key.pubText
	err := update(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("update err = %v, want the signature refusal", err)
	}
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if string(got) != "old binary" {
		t.Fatalf("binary = %q, want the old bytes (nothing written)", got)
	}
}

func TestUpdateRefusesAForeignSignature(t *testing.T) {
	keyA := newMinisignKey(t)
	keyB := newMinisignKey(t)
	asset := []byte("new binary bytes")
	srv := newUpdateSrv(t, "0.15.1", asset, checksumsFor(sha256Hex(asset)), keyB.sign(asset))
	dir := t.TempDir()
	bin := filepath.Join(dir, "rig")
	writeOld(t, bin)
	cfg := updateCfgFor(t, srv, "0.15.0", bin)
	cfg.key = keyA.pubText
	err := update(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("update err = %v, want the foreign-signature refusal", err)
	}
}

func TestUpdateRefusesAMismatchedKeyID(t *testing.T) {
	keyA := newMinisignKey(t)
	keyB := newMinisignKey(t)
	asset := []byte("new binary bytes")
	sig := keyA.sign(asset)
	lines := strings.SplitN(string(sig), "\n", 2)
	if len(lines) != 2 {
		t.Fatalf("sig has %d lines, want 2", len(lines))
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	replaced := append([]byte{}, decoded[:2]...)
	replaced = append(replaced, mustDecodePub(t, keyB.pubText)[2:10]...)
	replaced = append(replaced, decoded[10:]...)
	forged := lines[0] + "\n" + base64.StdEncoding.EncodeToString(replaced)
	srv := newUpdateSrv(t, "0.15.1", asset, checksumsFor(sha256Hex(asset)), []byte(forged))
	dir := t.TempDir()
	bin := filepath.Join(dir, "rig")
	writeOld(t, bin)
	cfg := updateCfgFor(t, srv, "0.15.0", bin)
	cfg.key = keyA.pubText
	err = update(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "key id") {
		t.Fatalf("update err = %v, want the key-id refusal", err)
	}
}

func mustDecodePub(t *testing.T, pubText string) []byte {
	t.Helper()
	lines := strings.SplitN(pubText, "\n", 2)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil {
		t.Fatalf("decode pub: %v", err)
	}
	return decoded
}

func TestVerifyMinisignRealFixtures(t *testing.T) {
	// Golden fixtures produced by minisign 0.11 (the apt package, the
	// release workflow's tool): the pubkey line, a default hashed ("ED")
	// signature, and a legacy ("Ed", raw) signature over the same file.
	// The fixture pins the real wire format: 2-byte algorithm tag +
	// 8-byte key id + 32-byte ed25519 key (42), and 2-byte tag + 8-byte
	// key id + 64-byte signature (74), with "ED" signing the BLAKE2b-512
	// digest.
	const (
		asset = "new binary bytes"
		pub   = "untrusted comment: minisign public key C4DF2D99EE1004A3\n" +
			"RWSjBBDumS3fxCdHn4jP6IlxNoGGaNjOwZAGy7ATCcoRYdJVu+uVwPgB"
		hashed = "untrusted comment: signature from minisign secret key\n" +
			"RUSjBBDumS3fxJbpKBT7jaSw+QeuiAyavmRPIcN9PgYHz2k7FD24JTyeDIXadZ/GWyxFeyyfulSH1AJBMBJ2yGzEbAHWyZwHpwA=\n" +
			"trusted comment: timestamp:1788816842\tfile:fixture-asset\thashed\n" +
			"rPFlG8HNkg7PyJEoRRpMxLeIPX3FDSc7NXLLZwy610TwQfjWvDlykW1T4C6LH3Ti1JUy7TGA1DcR3ybvrRkvBA=="
		legacy = "untrusted comment: signature from minisign secret key\n" +
			"RWSjBBDumS3fxLVTD1Im+1sRnJZ5NPBJDESSMcTepdpgdrgVrNJxxgNyPP4/63Y3qY+DR7n5oP0vXSe7z8Swjz7uWpbAaj+o1Qs=\n" +
			"trusted comment: timestamp:1788816842\tfile:fixture-asset\n" +
			"vuazr8KMJGAcjTQqlUgzcpdUeYB1ppMbpRIZ6w9qohN3khoLtPARlD138ixfwfoRQizeHa4L8kraY1oeh3a7Dw=="
	)
	if err := verifyMinisign(pub, []byte(asset), []byte(hashed)); err != nil {
		t.Fatalf("verify the default (hashed) release signature: %v", err)
	}
	if err := verifyMinisign(pub, []byte(asset), []byte(legacy)); err != nil {
		t.Fatalf("verify the legacy (raw) signature: %v", err)
	}
	if err := verifyMinisign(pub, []byte("tampered"), []byte(hashed)); err == nil {
		t.Fatal("a tampered asset must refuse")
	}
}

func TestVerifyMinisignRefusesTheInventedShortFormat(t *testing.T) {
	// The pre-0.24.3 format (40-byte key, 72-byte signature, raw data)
	// was invented: real minisign keys are 42 bytes and signatures 74.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keyID := pub[:8]
	pubText := "untrusted comment: minisign public key\n" +
		base64.StdEncoding.EncodeToString(append(append([]byte{}, keyID...), pub...))
	sig := ed25519.Sign(priv, []byte("new binary bytes"))
	sigText := "untrusted comment: signature from minisign secret key\n" +
		base64.StdEncoding.EncodeToString(append(append([]byte{}, sig...), keyID...))
	err = verifyMinisign(pubText, []byte("new binary bytes"), []byte(sigText))
	if err == nil || !strings.Contains(err.Error(), "want 42") {
		t.Fatalf("the invented short key format must refuse by name, got %v", err)
	}
}

func TestUpdateChecksumMatchesTheExactAssetField(t *testing.T) {
	key := newMinisignKey(t)
	asset := []byte("new binary bytes")
	checksums := fmt.Sprintf("%s  rig_linux_amd64_v2\n%s  rig_linux_amd64\n%s  rig_darwin_amd64\n",
		strings.Repeat("a", 64), sha256Hex(asset), strings.Repeat("0", 64))
	srv := newUpdateSrv(t, "0.15.1", asset, checksums, key.sign(asset))
	dir := t.TempDir()
	bin := filepath.Join(dir, "rig")
	writeOld(t, bin)
	cfg := updateCfgFor(t, srv, "0.15.0", bin)
	cfg.key = key.pubText
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
}
