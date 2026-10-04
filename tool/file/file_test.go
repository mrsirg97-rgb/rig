package file_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/tool/file"
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

func TestReadDescriptionNamesWhatEditChecksAgainst(t *testing.T) {
	desc := file.Read().Description()
	for _, want := range []string{
		"the native way to look at a file",
		"edit checks against its content",
		"exactly as edit will match it",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("the read description must say %q (read is the way to look at a file), got:\n%s", want, desc)
		}
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

const readCap = 1 << 20

var truncMarkerRe = regexp.MustCompile(`\n\[output truncated: (\d+) of (\d+) lines; continue at offset (\d+)\]$`)

var longLineMarkerRe = regexp.MustCompile(`\n\[output truncated: line (\d+) is longer than the 1 MiB cap; slice it with bash\]$`)

func splitTruncMarker(rep string) (body string, n, m, nextOffset int, capped bool) {
	mm := truncMarkerRe.FindStringSubmatch(rep)
	if mm == nil {
		return rep, 0, 0, 0, false
	}
	n, _ = strconv.Atoi(mm[1])
	m, _ = strconv.Atoi(mm[2])
	nextOffset, _ = strconv.Atoi(mm[3])
	return rep[:len(rep)-len(mm[0])], n, m, nextOffset, true
}

func TestReadWholeFileCapsByteIdenticalToTheSplitJoin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	var b strings.Builder
	for i := 0; b.Len() < readCap+4096; i++ {
		fmt.Fprintf(&b, "line %06d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	joined := strings.Join(lines, "\n")
	want := joined
	if len(want) > readCap {
		cut := strings.LastIndexByte(want[:readCap], '\n') + 1
		n := strings.Count(want[:cut], "\n")
		want = want[:cut] + fmt.Sprintf("\n[output truncated: %d of %d lines; continue at offset %d]", n, len(lines), n)
	}
	got, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{"path": path}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != want {
		t.Fatalf("the capped whole-file read drifted from the split-join contract:\n got %d bytes\nwant %d bytes", len(got), len(want))
	}
}

func TestReadWindowOfABigFileIsByteIdentical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	var b strings.Builder
	for i := 0; b.Len() < readCap+4096; i++ {
		fmt.Fprintf(&b, "line %06d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	end := 100 + 3
	if end > len(lines) {
		end = len(lines)
	}
	want := strings.Join(lines[100:end], "\n")
	got, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "offset": 100, "limit": 3,
	}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != want {
		t.Fatalf("the window drifted:\n got %q\nwant %q", got, want)
	}
}

func TestReadOneHugeLineFallsBackToARuneBoundary(t *testing.T) {
	dir := t.TempDir()
	straddle := filepath.Join(dir, "straddle.txt")
	if err := os.WriteFile(straddle, []byte(strings.Repeat("a", readCap-1)+"é"+"tail"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{"path": straddle}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	mm := longLineMarkerRe.FindStringSubmatch(got)
	if mm == nil {
		t.Fatalf("a readCap+1-byte file must come back capped, got %d bytes", len(got))
	}
	if mm[1] != "1" {
		t.Fatalf("the long-line marker must name line 1, got %q", mm[1])
	}
	body := got[:len(got)-len(mm[0])]
	if !utf8.ValidString(body) {
		t.Fatalf("the cap must not split a rune, got tail %q", body[len(body)-8:])
	}
	if body != strings.Repeat("a", readCap-1) {
		t.Fatalf("the rune straddling the cap must be dropped whole, got %d bytes", len(body))
	}
	aligned := filepath.Join(dir, "aligned.txt")
	if err := os.WriteFile(aligned, []byte(strings.Repeat("a", readCap)+"é"+"tail"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = file.Read().Exec(context.Background(), argsJSON(t, map[string]any{"path": aligned}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	mm = longLineMarkerRe.FindStringSubmatch(got)
	if mm == nil {
		t.Fatalf("a readCap+1-byte file must come back capped, got %d bytes", len(got))
	}
	if mm[1] != "1" {
		t.Fatalf("the long-line marker must name line 1, got %q", mm[1])
	}
	body = got[:len(got)-len(mm[0])]
	if !utf8.ValidString(body) {
		t.Fatalf("the cap must not split a rune, got tail %q", body[len(body)-8:])
	}
	if body != strings.Repeat("a", readCap) {
		t.Fatalf("the cap on a rune start must keep the body byte-identical, got %d bytes", len(body))
	}
}

func TestReadOneHugeLineCapsByteIdentical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one.txt")
	huge := strings.Repeat("x", readCap+8192)
	if err := os.WriteFile(path, []byte(huge), 0o644); err != nil {
		t.Fatal(err)
	}
	want := huge[:readCap] + "\n[output truncated: line 1 is longer than the 1 MiB cap; slice it with bash]"
	got, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{"path": path}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != want {
		t.Fatalf("the huge-line cap drifted: got %d bytes, want %d", len(got), len(want))
	}
}

func TestReadBigFileDoesNotAllocateTheWholeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	size := 64 << 20
	if err := os.WriteFile(path, []byte(strings.Repeat("x\n", size/2)), 0o644); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{"path": path}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	runtime.ReadMemStats(&after)
	if !truncMarkerRe.MatchString(got) {
		t.Fatalf("a %d-byte file must come back capped with the line-facts marker, got %d bytes", size, len(got))
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	const bound = 16 << 20
	if allocated > bound {
		t.Fatalf("the read allocated %d bytes for a %d-byte file, want <= %d (the cap is %d; the whole file must not be materialised)", allocated, size, bound, readCap)
	}
}

func TestReadBigFileRangesReassembleExactly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	var b strings.Builder
	for b.Len() < 3*readCap+4096 {
		fmt.Fprintf(&b, "line %08d\n", b.Len())
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	offset := 0
	for reads := 0; ; reads++ {
		if reads >= 10 {
			t.Fatal("the range walk did not terminate")
		}
		rep, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{"path": path, "offset": offset}))
		if err != nil {
			t.Fatalf("read at offset %d: %v", offset, err)
		}
		body, n, _, nextOffset, capped := splitTruncMarker(rep)
		got.WriteString(body)
		if !capped {
			break
		}
		if n == 0 {
			t.Fatalf("a capped read reported 0 complete lines at offset %d; the walk cannot advance", offset)
		}
		if nextOffset != offset+n {
			t.Fatalf("the marker's next offset %d must be offset+n (%d) at offset %d", nextOffset, offset+n, offset)
		}
		offset = nextOffset
	}
	if got.String() != string(want) {
		t.Fatalf("the marker-guided ranges reassembled to %d bytes, want %d", got.Len(), len(want))
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	for _, a := range [][]string{
		{"init"},
		{"config", "user.name", "rig test"},
		{"config", "user.email", "rig@test"},
		{"config", "commit.gpgsign", "false"},
	} {
		git(t, dir, a...)
	}
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "--allow-empty", "-m", msg)
}

func TestReadDiffShowsTheHunk(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(oldWd) })
	initRepo(t, dir)
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "base")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{"path": path, "diff": true}))
	if err != nil {
		t.Fatalf("read diff: %v", err)
	}
	if !strings.Contains(got, "@@") || !strings.Contains(got, "+two") {
		t.Fatalf("a modified file must show its hunk:\n%s", got)
	}
}

func TestReadDiffShowsTheHunkWithTheEditStaged(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(oldWd) })
	initRepo(t, dir)
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "base")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "note.txt")
	got, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{"path": path, "diff": true}))
	if err != nil {
		t.Fatalf("read diff: %v", err)
	}
	if !strings.Contains(got, "@@") || !strings.Contains(got, "+two") {
		t.Fatalf("a staged edit must still show its hunk against HEAD:\n%s", got)
	}
}

func TestReadDiffShowsAnAddedFileStagedWhole(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(oldWd) })
	initRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "base")
	path := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(path, []byte("fresh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "new.txt")
	got, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{"path": path, "diff": true}))
	if err != nil {
		t.Fatalf("read diff: %v", err)
	}
	if strings.Contains(got, "no changes") {
		t.Fatalf("an added file staged but never committed must diff against HEAD whole:\n%s", got)
	}
	if !strings.Contains(got, "new file") || !strings.Contains(got, "+fresh") {
		t.Fatalf("the staged add must show as a new file:\n%s", got)
	}
}

func TestReadDiffCleanSaysNoChanges(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(oldWd) })
	initRepo(t, dir)
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "base")
	got, err := file.Read().Exec(context.Background(), argsJSON(t, map[string]any{"path": path, "diff": true}))
	if err != nil {
		t.Fatalf("read diff: %v", err)
	}
	if !strings.HasSuffix(got, "\n\nno changes") {
		t.Fatalf("a clean file must append 'no changes':\n%s", got)
	}
}
