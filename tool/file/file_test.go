package file_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/tool/file"
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

func TestReadNotesStaleObservation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := core.NewSession()
	ctx := core.WithSession(context.Background(), session)
	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(path, []byte("second"), 0o644); err != nil { // external change, no session call
		t.Fatal(err)
	}
	got, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(got, "changed since your observation") {
		t.Fatalf("a stale observation must be named, got %q", got)
	}
	if !strings.Contains(got, "second") {
		t.Fatalf("the fresh content must still ride the note, got %q", got)
	}
}

func TestReadFreshObservationStaysQuiet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := core.NewSession()
	ctx := core.WithSession(context.Background(), session)
	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read: %v", err)
	}
	got, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(got, "changed since your observation") {
		t.Fatalf("a fresh read must stay quiet, got %q", got)
	}
}

func TestReadRefusesUnknownArg(t *testing.T) {
	_, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{"path": "/tmp/x", "extra": 1}))
	if err == nil {
		t.Fatal("unknown args must be refused")
	}
}

func readLines(t *testing.T, content string) []string {
	t.Helper()
	return strings.Split(content, "\n")
}

func TestReadOffsetLimitReturnsTheRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	content := strings.Join([]string{
		"line 00", "line 01", "line 02", "line 03", "line 04",
		"line 05", "line 06", "line 07", "line 08", "line 09",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "offset": 2, "limit": 3,
	}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := strings.Join([]string{"line 02", "line 03", "line 04"}, "\n")
	if got != want {
		t.Fatalf("offset/limit range = %q, want %q", got, want)
	}
}

func TestReadOffsetPastTheEndRefusesLoud(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "offset": 9,
	}))
	if err == nil || !strings.Contains(err.Error(), "past the end") {
		t.Fatalf("an offset past the end must refuse loud naming the file's lines, got %v", err)
	}
}

func TestReadOffsetNegativeRefusesLoud(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "offset": -1,
	}))
	if err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("a negative offset must refuse loud, got %v", err)
	}
}

func TestReadLimitNegativeRefusesLoud(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "limit": -1,
	}))
	if err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("a negative limit must refuse loud, got %v", err)
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

	if err := os.WriteFile(path, []byte("version two"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "version", "new": "draft",
	}))
	if err == nil || !strings.Contains(err.Error(), "the file changed since the read") {
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

func TestDriftCheckIsPathSpellingInsensitive(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(oldWd) })

	path := "code.txt"
	if err := os.WriteFile(path, []byte("version one"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := core.NewSession()
	ctx := core.WithSession(context.Background(), session)

	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(path, []byte("version two"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": "." + string(os.PathSeparator) + "code.txt",
		"old":  "version", "new": "draft",
	})); err == nil || !strings.Contains(err.Error(), "the file changed since the read") {
		t.Fatalf("drift check bypassed by path spelling, got %v", err)
	}
}

func TestEditWithoutPriorReadProceeds(t *testing.T) {

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

func driftSetup(t *testing.T, path, content string) (context.Context, *core.Session) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	session := core.NewSession()
	ctx := core.WithSession(context.Background(), session)
	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read: %v", err)
	}
	return ctx, session
}

func TestDriftRefusalShowsASmallDriftWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	ctx, _ := driftSetup(t, path, "alpha\nbeta\ngamma\n")
	if err := os.WriteFile(path, []byte("alpha\nBETA\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "beta", "new": "beta2",
	}))
	if err == nil {
		t.Fatal("the drift must refuse")
	}
	msg := err.Error()
	for _, want := range []string{
		"edit: the file changed since the read:",
		"--- as read",
		"+++ on disk",
		"-beta",
		"+BETA",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal must carry the diff, got:\n%s", msg)
		}
	}
	if strings.Contains(msg, "more lines") {
		t.Fatalf("a small drift shows whole, no elision:\n%s", msg)
	}
}

func TestDriftRefusalCapsARewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	var old strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&old, "old line %02d\n", i)
	}
	ctx, _ := driftSetup(t, path, old.String())
	var nw strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&nw, "new line %02d\n", i)
	}
	if err := os.WriteFile(path, []byte(nw.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "old", "new": "new",
	}))
	if err == nil {
		t.Fatal("the drift must refuse")
	}
	lines := strings.Split(err.Error(), "\n")

	if want := 1 + 20 + 1; len(lines) != want {
		t.Fatalf("the refusal caps at 20 diff lines plus the loud marker, got %d lines:\n%s", len(lines), err.Error())
	}

	if last := lines[len(lines)-1]; last != "… 63 more lines" {
		t.Fatalf("the marker must name the elided count, got %q", last)
	}
}
