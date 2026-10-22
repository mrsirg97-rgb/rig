package file_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/tool/file"
)

func TestAliasedReadThenRealPathEditRefusesTheDrift(t *testing.T) {
	dir := realTemp(t)
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	realPath := filepath.Join(real, "f.txt")
	if err := os.WriteFile(realPath, []byte("version one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	aliased := filepath.Join(dir, "link", "f.txt")
	session := core.NewSession()
	ctx := core.WithSession(context.Background(), session)
	if _, err := file.NewRead().Exec(ctx, argsJSON(t, map[string]any{"path": aliased})); err != nil {
		t.Fatalf("read via the alias: %v", err)
	}
	if err := os.WriteFile(realPath, []byte("version two"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.NewEdit().Exec(ctx, argsJSON(t, map[string]any{
		"path": realPath, "old": "version", "new": "draft",
	}))
	if err == nil || !strings.Contains(err.Error(), "the file changed since the read") {
		t.Fatalf("the aliased read and the real-path edit are one file, so the edit must refuse with the drift voice, got %v", err)
	}
	data, err := os.ReadFile(realPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "version two" {
		t.Fatalf("a licensed edit must never clobber the external change, got %q", data)
	}
}

func TestRealReadThenAliasedEditRefusesTheDrift(t *testing.T) {
	dir := realTemp(t)
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	realPath := filepath.Join(real, "f.txt")
	if err := os.WriteFile(realPath, []byte("version one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	aliased := filepath.Join(dir, "link", "f.txt")
	session := core.NewSession()
	ctx := core.WithSession(context.Background(), session)
	if _, err := file.NewRead().Exec(ctx, argsJSON(t, map[string]any{"path": realPath})); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(realPath, []byte("version two"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.NewEdit().Exec(ctx, argsJSON(t, map[string]any{
		"path": aliased, "old": "version", "new": "draft",
	}))
	if err == nil || !strings.Contains(err.Error(), "the file changed since the read") {
		t.Fatalf("the real-path read and the aliased edit are one file, so the edit must refuse with the drift voice, got %v", err)
	}
	data, err := os.ReadFile(realPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "version two" {
		t.Fatalf("a licensed edit must never clobber the external change, got %q", data)
	}
}

func TestDotDotThroughASymlinkReadsTheResolvedFile(t *testing.T) {
	dir := realTemp(t)
	if err := os.MkdirAll(filepath.Join(dir, "x", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x", "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("top"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "x", "sub"), filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	spelled := dir + "/alias/../keep.txt"
	got, err := file.NewRead().Exec(context.Background(), argsJSON(t, map[string]any{"path": spelled}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "keep" {
		t.Fatalf("alias/../keep.txt must be the file the OS resolves (x/keep.txt), got %q", got)
	}
}

func TestWriteThroughAliasedSpellingLandsWhereTheOsResolves(t *testing.T) {
	dir := realTemp(t)
	if err := os.MkdirAll(filepath.Join(dir, "x", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "x", "sub"), filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	spelled := dir + "/alias/../made.txt"
	resolved := filepath.Join(dir, "x", "made.txt")
	session := core.NewSession()
	ctx := core.WithSession(context.Background(), session)
	got, err := file.NewWrite().Exec(ctx, argsJSON(t, map[string]any{"path": spelled, "content": "made"}))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if want := "wrote 4 bytes to " + resolved; got != want {
		t.Fatalf("the write must land at the resolved path and say so, got %q want %q", got, want)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatalf("the file must exist where the OS resolves the spelling: %v", err)
	}
	if string(data) != "made" {
		t.Fatalf("content = %q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "made.txt")); err == nil {
		t.Fatal("the lexical mis-point must not exist")
	}
	got, err = file.NewRead().Exec(ctx, argsJSON(t, map[string]any{"path": resolved}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(got, "changed since your observation") {
		t.Fatalf("the write's license must key the resolved path, got %q", got)
	}
}
