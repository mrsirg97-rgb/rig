package file_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/looper/core"
	"github.com/mrsirg97-rgb/looper/tool/file"
)

func argsJSON(t *testing.T, args map[string]any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReadReturnsContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("read me"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{"path": path}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "read me" {
		t.Fatalf("content = %q", got)
	}
}

func TestReadRecordsProvenanceWhenThreaded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("read me"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := core.NewSession()
	ctx := core.WithSession(context.Background(), session)
	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read: %v", err)
	}
	state, ok := session.Files[path]
	if !ok {
		t.Fatal("read must record file provenance for the threaded session")
	}
	if state.Hash == "" || state.Mtime <= 0 {
		t.Fatalf("provenance incomplete: %+v", state)
	}
}

func TestReadRefusesUnknownArg(t *testing.T) {
	_, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{"path": "/tmp/x", "extra": 1}))
	if err == nil {
		t.Fatal("unknown args must be refused")
	}
}

func TestWriteCreatesAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	tool := file.Write()
	if _, err := tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "content": "first",
	})); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "content": "second",
	})); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second" {
		t.Fatalf("content = %q, want second", data)
	}
}

func TestWriteRefusesMissingParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no/such/dir/note.txt")
	_, err := file.Write().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "content": "x",
	}))
	if err == nil {
		t.Fatal("write into a missing parent dir must fail loudly")
	}
}

func TestEditReplacesExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("alpha beta gamma"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := file.Edit().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "old": "beta", "new": "BETA",
	}))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if got == "" {
		t.Fatal("edit must report what it did")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha BETA gamma" {
		t.Fatalf("content = %q", data)
	}
}

func TestEditAmbiguityFailsLoud(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("x x x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Edit().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "old": "x", "new": "y",
	}))
	if err == nil || !strings.Contains(err.Error(), "3") {
		t.Fatalf("ambiguous edit must name the occurrence count, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "x x x" {
		t.Fatal("a failed edit must not mutate the file")
	}
}

func TestEditMissingFailsLoud(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Edit().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "old": "absent", "new": "x",
	}))
	if err == nil {
		t.Fatal("edit of an absent string must fail loudly")
	}
}

func TestEditAfterExternalChangeFailsLoud(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("version one"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := core.NewSession()
	ctx := core.WithSession(context.Background(), session)

	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read: %v", err)
	}
	// external change after the last read
	if err := os.WriteFile(path, []byte("version two"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "version", "new": "draft",
	}))
	if err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("edit-after-external-change must fail loudly naming the drift, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "version two" {
		t.Fatalf("drifted file mutated: %q", data)
	}
}

func TestEditWithoutPriorReadProceeds(t *testing.T) {
	// no provenance baseline means nothing was observed; the edit goes
	// through, loudly reported.
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := file.Edit().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "old": "one", "new": "two",
	}))
	if err != nil {
		t.Fatalf("edit without prior read: %v", err)
	}
	if got == "" {
		t.Fatal("edit must report what it did")
	}
}

func TestEditRecordsFreshProvenance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := core.NewSession()
	ctx := core.WithSession(context.Background(), session)
	if _, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "one", "new": "two",
	})); err != nil {
		t.Fatalf("edit: %v", err)
	}
	state, ok := session.Files[path]
	if !ok || state.Hash == "" {
		t.Fatal("edit must record fresh, complete provenance for subsequent edits")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// a second edit against the just-recorded state must now succeed
	if _, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "two", "new": "three",
	})); err != nil {
		t.Fatalf("second edit: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "three" {
		t.Fatalf("second edit content = %q, want three", data)
	}
}
