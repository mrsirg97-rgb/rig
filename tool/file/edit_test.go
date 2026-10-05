package file_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/tool/file"
)

func TestEditSingleChangeReplacesExactlyOnce(t *testing.T) {
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

func TestEditReplyNamesThePathAndBytesReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("one two three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read: %v", err)
	}
	got, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "two three", "new": "2 3",
	}))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	want := "edited " + path + ": replaced 9 byte(s)"
	if got != want {
		t.Fatalf("the reply is one line, the path and the bytes replaced:\n got %q\nwant %q", got, want)
	}
}

func TestEditAmbiguousOldNamesTheCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("x x x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read: %v", err)
	}
	_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "x", "new": "y",
	}))
	if err == nil || !strings.Contains(err.Error(), "matched 3 times, want exactly 1") {
		t.Fatalf("an ambiguous old must refuse naming its match count, got %v", err)
	}
	if !strings.Contains(err.Error(), "nothing landed") {
		t.Fatalf("the refusal must say nothing landed, got %v", err)
	}
	if strings.Contains(err.Error(), "chunk") {
		t.Fatalf("there is no chunk grammar to name, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "x x x" {
		t.Fatal("a failed edit must not mutate the file")
	}
}

func TestEditAbsentOldRefusesLoud(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read: %v", err)
	}
	_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "absent", "new": "x",
	}))
	if err == nil || !strings.Contains(err.Error(), "matched 0 times") {
		t.Fatalf("an absent old must refuse naming the miss, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatal("a refused edit must not mutate the file")
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
		"path": "." + string(os.PathSeparator) + "code.txt", "old": "version", "new": "draft",
	})); err == nil || !strings.Contains(err.Error(), "the file changed since the read") {
		t.Fatalf("drift check bypassed by path spelling, got %v", err)
	}
}

func TestEditDescriptionNamesTheGuideline(t *testing.T) {
	desc := file.Edit().Description()
	for _, want := range []string{
		"put enough of it in `old` to match exactly once",
		"one change per call",
		"several calls in one turn",
		"land in the order they are listed",
		"returns the file's text instead of refusing",
		"the next call edits it",
		"the file changed since your read",
		"the path and the bytes replaced",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("the edit description must say %q, got:\n%s", want, desc)
		}
	}
}

func TestEditSchemaTakesPathOldNew(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(file.Edit().Schema(), &m); err != nil {
		t.Fatalf("schema: %v", err)
	}
	req, _ := m["required"].([]any)
	if len(req) != 3 || req[0] != "path" || req[1] != "old" || req[2] != "new" {
		t.Fatalf("the schema must require path, old and new, got %v", req)
	}
	props, _ := m["properties"].(map[string]any)
	for _, gone := range []string{"edits"} {
		if _, ok := props[gone]; ok {
			t.Fatalf("the chunk list %q must be gone from the schema", gone)
		}
	}
	for _, want := range []string{"old", "new"} {
		if _, ok := props[want].(map[string]any); !ok {
			t.Fatalf("%q must be a property of its own, got %v", want, props[want])
		}
	}
}

func TestEditRefusesLegacyEditsArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Edit().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "edits": []map[string]string{{"old": "one", "new": "two"}},
	}))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("the chunk list is gone from the schema; the legacy shape must refuse as args, got %v", err)
	}
}

func TestEditWithoutPriorReadAppliesWhenOldMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := core.NewSession()
	ctx := core.WithSession(context.Background(), session)
	got, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "one", "new": "1",
	}))
	if err != nil {
		t.Fatalf("an unread file whose old matches once applies: %v", err)
	}
	if !strings.Contains(got, "edited "+path) {
		t.Fatalf("the reply must name the path and the total, got %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "1\ntwo\n" {
		t.Fatalf("content = %q, want 1\\ntwo\\n", data)
	}
	if _, ok := session.Files[path]; !ok {
		t.Fatal("the applied edit must refresh the observation")
	}
}

func TestEditWithoutPriorReadMismatchReturnsTheFileText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	content := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	got, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "absent", "new": "x",
	}))
	if err != nil {
		t.Fatalf("a mismatch on an unread file hands back the text instead of refusing: %v", err)
	}
	want := content + "\n[edit: " + path + " was not read this session; its text is above, now edit it]"
	if got != want {
		t.Fatalf("the reply must be the file's text ending with the marker:\n got %q\nwant %q", got, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatal("the teaching reply must not mutate the file")
	}
}

func TestEditWithoutPriorReadAmbiguousReturnsTheFileText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("x y x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	got, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "x", "new": "z",
	}))
	if err != nil {
		t.Fatalf("an ambiguous old on an unread file hands back the text too: %v", err)
	}
	if !strings.HasSuffix(got, "\n[edit: "+path+" was not read this session; its text is above, now edit it]") {
		t.Fatalf("the ambiguous reply must end with the marker, got %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "x y x" {
		t.Fatal("the teaching reply must not mutate the file")
	}
}

func TestEditUnreadTeachesOnceOnAMiss(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	content := "alpha\nbeta\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	got, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "absent", "new": "x",
	}))
	if err != nil {
		t.Fatalf("a miss on an unread file teaches instead of refusing: %v", err)
	}
	marker := "\n[edit: " + path + " was not read this session; its text is above, now edit it]"
	if strings.Count(got, marker) != 1 {
		t.Fatalf("the miss must teach once, got %d markers:\n%s", strings.Count(got, marker), got)
	}
	if body := strings.TrimSuffix(got, marker); body != content {
		t.Fatalf("the teaching must be the whole file exactly once, got %q", body)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatal("the teaching reply must not mutate the file")
	}
}

func TestEditWithoutPriorReadReplyIsReadPlusTheMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fromRead, err := file.Read().Exec(core.WithSession(context.Background(), core.NewSession()), argsJSON(t, map[string]any{"path": path}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	got, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "absent", "new": "x",
	}))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	want := fromRead + "\n[edit: " + path + " was not read this session; its text is above, now edit it]"
	if got != want {
		t.Fatalf("the teaching reply must be the read's bytes plus the marker:\n got %q\nwant %q", got, want)
	}
}

func TestEditWithoutPriorReadReplyCapsLikeRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	var b strings.Builder
	for b.Len() < readCap+4096 {
		fmt.Fprintf(&b, "line %06d\n", b.Len())
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	got, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "absent", "new": "x",
	}))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	editMarker := "\n[edit: " + path + " was not read this session; its text is above, now edit it]"
	if !strings.HasSuffix(got, editMarker) {
		t.Fatalf("the teaching reply must end with the marker, got %q", got)
	}
	if !strings.Contains(got, "[output truncated: ") {
		t.Fatalf("a file over the cap must carry read's truncation marker, got %d bytes", len(got))
	}
	fromRead, err := file.Read().Exec(core.WithSession(context.Background(), core.NewSession()), argsJSON(t, map[string]any{"path": path}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if body := got[:len(got)-len(editMarker)]; body != fromRead {
		t.Fatalf("the capped teaching reply must be the read's bytes plus the marker, got %d bytes want %d", len(body), len(fromRead))
	}
}

func TestEditAfterUnreadTeachingReplyIsDriftChecked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "absent", "new": "x",
	})); err != nil {
		t.Fatalf("the teaching reply: %v", err)
	}
	if err := os.WriteFile(path, []byte("alpha\nBETA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "beta", "new": "gamma",
	}))
	if err == nil || !strings.Contains(err.Error(), "the file changed since the read") {
		t.Fatalf("the edit that follows the teaching reply must be drift-checked like any other, got %v", err)
	}
}

func TestEditAfterUnreadTeachingReplyAppliesWithoutARead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "absent", "new": "x",
	})); err != nil {
		t.Fatalf("the teaching reply: %v", err)
	}
	if _, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "beta", "new": "gamma",
	})); err != nil {
		t.Fatalf("the follow-up edit must apply without a separate read: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha\ngamma\n" {
		t.Fatalf("content = %q, want alpha\\ngamma\\n", data)
	}
}

func TestEditEmptyOldRefusesAheadOfTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{
		core.WithSession(context.Background(), core.NewSession()),
		context.Background(),
	} {
		_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
			"path": path, "old": "", "new": "x",
		}))
		if err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("an empty old is an args problem, unread file or not, got %v", err)
		}
	}
}

func TestEditWithoutPriorReadMissingFileRefusesLoud(t *testing.T) {
	ctx := core.WithSession(context.Background(), core.NewSession())
	_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": filepath.Join(t.TempDir(), "absent.txt"), "old": "x", "new": "y",
	}))
	if err == nil || !strings.Contains(err.Error(), "absent.txt") {
		t.Fatalf("a missing file is not a teaching moment, got %v", err)
	}
}

func TestEditWithoutPriorReadProceedsStandalone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := file.Edit().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "old": "one", "new": "two",
	}))
	if err != nil {
		t.Fatalf("a standalone exec carries no session and so no license to check: %v", err)
	}
	if got == "" {
		t.Fatal("edit must report what it did")
	}
}

func TestEditAfterWriteProceedsThreaded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := file.Write().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "content": "one",
	})); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "one", "new": "two",
	})); err != nil {
		t.Fatalf("write mints the edit license: %v", err)
	}
}

func TestEditOldPlusNewAtTheReadCeilingRefuses(t *testing.T) {
	_, err := file.Edit().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": filepath.Join(t.TempDir(), "absent.txt"),
		"old":  "a", "new": strings.Repeat("x", readCap-1),
	}))
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(readCap)) {
		t.Fatalf("old plus new at the read ceiling must refuse naming it, got %v", err)
	}
}

func TestEditOldPlusNewUnderTheReadCeilingApplies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "a", "new": strings.Repeat("x", readCap-2),
	})); err != nil {
		t.Fatalf("old plus new under the read ceiling applies: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != readCap-1 {
		t.Fatalf("content = %d bytes, want %d", len(data), readCap-1)
	}
}

func TestDriftDiffKeysOnTheSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("A content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctxA := core.WithSession(context.Background(), core.NewSession())
	ctxB := core.WithSession(context.Background(), core.NewSession())
	if _, err := file.Read().Exec(ctxA, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read A: %v", err)
	}
	if err := os.WriteFile(path, []byte("B content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Read().Exec(ctxB, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read B: %v", err)
	}
	if err := os.WriteFile(path, []byte("C content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Edit().Exec(ctxA, argsJSON(t, map[string]any{
		"path": path, "old": "A content", "new": "X",
	}))
	if err == nil || !strings.Contains(err.Error(), "the file changed since the read") {
		t.Fatalf("session A's edit must refuse naming the drift, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "-A content") || !strings.Contains(msg, "+C content") {
		t.Fatalf("the drift must diff against session A's own observation, got:\n%s", msg)
	}
	if strings.Contains(msg, "B content") {
		t.Fatalf("session B's observation must not bleed into session A's refusal, got:\n%s", msg)
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
	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "one", "new": "two",
	})); err != nil {
		t.Fatalf("edit: %v", err)
	}
	state, ok := session.Files[path]
	if !ok || state.Hash == "" {
		t.Fatal("edit must record fresh, complete provenance for subsequent edits")
	}
	if _, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "two", "new": "three",
	})); err != nil {
		t.Fatalf("second edit: %v", err)
	}
	data, err := os.ReadFile(path)
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
