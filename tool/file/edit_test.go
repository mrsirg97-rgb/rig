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

func hunks(pairs ...[2]string) []map[string]string {
	out := make([]map[string]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, map[string]string{"old": p[0], "new": p[1]})
	}
	return out
}

func TestEditSingleChangeReplacesExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("alpha beta gamma"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := file.Edit().Exec(context.Background(), argsJSON(t, map[string]any{
		"path":  path,
		"edits": hunks([2]string{"beta", "BETA"}),
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

func TestEditReplyIsOneLinePerHunkThenPathAndTotal(t *testing.T) {
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
		"path":  path,
		"edits": hunks([2]string{"two", "TWO"}, [2]string{"three", "THREE"}),
	}))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	want := "hunk 1: replaced 3 byte(s)\nhunk 2: replaced 5 byte(s)\nedited " + path + ": replaced 8 byte(s)"
	if got != want {
		t.Fatalf("the reply is one line per hunk then the path and total:\n got %q\nwant %q", got, want)
	}
}

func TestEditNHunksEqualNSequentialEdits(t *testing.T) {
	for n := 1; n <= 6; n++ {
		toks := make([]string, n)
		var b strings.Builder
		for i := range toks {
			toks[i] = fmt.Sprintf("tok%02d", i)
			b.WriteString(toks[i] + "\n")
		}
		content := b.String()
		pathA := filepath.Join(t.TempDir(), "code.txt")
		pathB := filepath.Join(t.TempDir(), "code.txt")
		if err := os.WriteFile(pathA, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pathB, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		sessA, sessB := core.NewSession(), core.NewSession()
		ctxA := core.WithSession(context.Background(), sessA)
		ctxB := core.WithSession(context.Background(), sessB)
		if _, err := file.Read().Exec(ctxA, argsJSON(t, map[string]any{"path": pathA})); err != nil {
			t.Fatalf("read A: %v", err)
		}
		if _, err := file.Read().Exec(ctxB, argsJSON(t, map[string]any{"path": pathB})); err != nil {
			t.Fatalf("read B: %v", err)
		}
		edits := make([]map[string]string, n)
		for i := range toks {
			edits[i] = map[string]string{"old": toks[i], "new": "TOK" + toks[i][3:]}
		}
		reply, err := file.Edit().Exec(ctxA, argsJSON(t, map[string]any{
			"path": pathA, "edits": edits,
		}))
		if err != nil {
			t.Fatalf("n=%d multi-hunk: %v", n, err)
		}
		if lines := strings.Count(reply, "\n") + 1; lines != n+1 {
			t.Fatalf("n=%d: the reply must carry one line per hunk plus the total, got %d lines:\n%s", n, lines, reply)
		}
		for i := range toks {
			if _, err := file.Edit().Exec(ctxB, argsJSON(t, map[string]any{
				"path":  pathB,
				"edits": hunks([2]string{toks[i], "TOK" + toks[i][3:]}),
			})); err != nil {
				t.Fatalf("n=%d sequential hunk %d: %v", n, i+1, err)
			}
		}
		dataA, err := os.ReadFile(pathA)
		if err != nil {
			t.Fatal(err)
		}
		dataB, err := os.ReadFile(pathB)
		if err != nil {
			t.Fatal(err)
		}
		if string(dataA) != string(dataB) {
			t.Fatalf("n=%d: the multi-hunk result drifted from the sequential edits", n)
		}
		if sessA.Files[pathA].Hash != sessB.Files[pathB].Hash {
			t.Fatalf("n=%d: the recorded provenance drifted between the two shapes", n)
		}
	}
}

func TestEditAmbiguousHunkNamesTheCount(t *testing.T) {
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
		"path":  path,
		"edits": hunks([2]string{"x", "y"}),
	}))
	if err == nil || !strings.Contains(err.Error(), "hunk 1 of 1") || !strings.Contains(err.Error(), "matched 3 times") {
		t.Fatalf("an ambiguous hunk must name the hunk and its match count, got %v", err)
	}
	if !strings.Contains(err.Error(), "nothing landed") {
		t.Fatalf("the refusal must say nothing landed, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "x x x" {
		t.Fatal("a failed edit must not mutate the file")
	}
}

func TestEditAbsentHunkRefusesLoud(t *testing.T) {
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
		"path":  path,
		"edits": hunks([2]string{"absent", "x"}),
	}))
	if err == nil || !strings.Contains(err.Error(), "hunk 1 of 1") || !strings.Contains(err.Error(), "matched 0 times") {
		t.Fatalf("an absent hunk must refuse naming the hunk and the miss, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatal("a refused edit must not mutate the file")
	}
}

func TestEditHunkTwoConsumingHunkOneTextRefusesLandingNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("ab\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read: %v", err)
	}
	_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path":  path,
		"edits": hunks([2]string{"ab", "a"}, [2]string{"ab", "Z"}),
	}))
	if err == nil || !strings.Contains(err.Error(), "hunk 2 of 2") || !strings.Contains(err.Error(), "matched 0 times") {
		t.Fatalf("hunk 2 must be validated against the content hunk 1 leaves and refuse by name, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ab\n" {
		t.Fatalf("the refused call must land nothing, got %q", data)
	}
}

func TestEditHunkTwoMatchingTwiceAfterHunkOneNamesTheCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("xay\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read: %v", err)
	}
	_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path":  path,
		"edits": hunks([2]string{"a", "aa"}, [2]string{"a", "Z"}),
	}))
	if err == nil || !strings.Contains(err.Error(), "hunk 2 of 2") || !strings.Contains(err.Error(), "matched 2 times") {
		t.Fatalf("hunk 2 matching twice after hunk 1 must name the count, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "xay\n" {
		t.Fatalf("the refused call must land nothing, got %q", data)
	}
}

func TestEditHunkMatchesOnlyAfterHunkOneApplies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("xay\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": path})); err != nil {
		t.Fatalf("read: %v", err)
	}
	got, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path":  path,
		"edits": hunks([2]string{"a", "bee"}, [2]string{"bee", "Z"}),
	}))
	if err != nil {
		t.Fatalf("a later hunk may match text an earlier one created: %v", err)
	}
	if !strings.Contains(got, "hunk 1: replaced 1 byte(s)") || !strings.Contains(got, "hunk 2: replaced 3 byte(s)") {
		t.Fatalf("the reply must carry each hunk's own count, got %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "xZy\n" {
		t.Fatalf("content = %q, want xZy\\n", data)
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
		"path":  path,
		"edits": hunks([2]string{"version", "draft"}),
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
		"path":  "." + string(os.PathSeparator) + "code.txt",
		"edits": hunks([2]string{"version", "draft"}),
	})); err == nil || !strings.Contains(err.Error(), "the file changed since the read") {
		t.Fatalf("drift check bypassed by path spelling, got %v", err)
	}
}

func TestEditDescriptionNamesTheGuideline(t *testing.T) {
	desc := file.Edit().Description()
	for _, want := range []string{
		"put enough of the file in each old to match exactly once",
		"several changes to one file go in one call, applied in order, all or none",
		"comes back as the file's text instead of a refusal",
		"the next call edits it",
		"the file changed since your read",
		"one line per hunk",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("the edit description must say %q, got:\n%s", want, desc)
		}
	}
}

func TestEditSchemaDropsTopLevelOldAndNew(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(file.Edit().Schema(), &m); err != nil {
		t.Fatalf("schema: %v", err)
	}
	req, _ := m["required"].([]any)
	if len(req) != 2 || req[0] != "path" || req[1] != "edits" {
		t.Fatalf("the schema must require path and edits only, got %v", req)
	}
	props, _ := m["properties"].(map[string]any)
	for _, gone := range []string{"old", "new"} {
		if _, ok := props[gone]; ok {
			t.Fatalf("the top-level %q must be gone from the schema", gone)
		}
	}
	edits, ok := props["edits"].(map[string]any)
	if !ok {
		t.Fatal("the edits property is missing")
	}
	items, ok := edits["items"].(map[string]any)
	if !ok {
		t.Fatal("the edits property must carry item shapes")
	}
	ireq, _ := items["required"].([]any)
	if len(ireq) != 2 || ireq[0] != "old" || ireq[1] != "new" {
		t.Fatalf("each hunk must require old and new, got %v", ireq)
	}
}

func TestEditRefusesLegacyOldNewArgs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Edit().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "old": "one", "new": "two",
	}))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("the top-level old and new are gone from the schema; the legacy shape must refuse as args, got %v", err)
	}
}

func TestEditWithoutPriorReadAppliesWhenAllHunksMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := core.NewSession()
	ctx := core.WithSession(context.Background(), session)
	got, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path":  path,
		"edits": hunks([2]string{"one", "1"}, [2]string{"two", "2"}),
	}))
	if err != nil {
		t.Fatalf("an unread file with every hunk matching once applies: %v", err)
	}
	if !strings.Contains(got, "edited "+path) {
		t.Fatalf("the reply must name the path and the total, got %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "1\n2\n" {
		t.Fatalf("content = %q, want 1\\n2\\n", data)
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
		"path":  path,
		"edits": hunks([2]string{"alpha", "A"}, [2]string{"absent", "x"}),
	}))
	if err != nil {
		t.Fatalf("a mismatch on an unread file hands back the text instead of a refusal: %v", err)
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
		"path":  path,
		"edits": hunks([2]string{"x", "z"}),
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

func TestEditUnreadTeachesOnceOnAnyMiss(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	content := "alpha\nbeta\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	got, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path":  path,
		"edits": hunks([2]string{"alpha", "A"}, [2]string{"absent", "x"}),
	}))
	if err != nil {
		t.Fatalf("any miss on an unread file teaches instead of refusing: %v", err)
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
		"path":  path,
		"edits": hunks([2]string{"absent", "x"}),
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
		"path":  path,
		"edits": hunks([2]string{"absent", "x"}),
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
		"path":  path,
		"edits": hunks([2]string{"absent", "x"}),
	})); err != nil {
		t.Fatalf("the teaching reply: %v", err)
	}
	if err := os.WriteFile(path, []byte("alpha\nBETA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path":  path,
		"edits": hunks([2]string{"beta", "gamma"}),
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
		"path":  path,
		"edits": hunks([2]string{"absent", "x"}),
	})); err != nil {
		t.Fatalf("the teaching reply: %v", err)
	}
	if _, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path":  path,
		"edits": hunks([2]string{"beta", "gamma"}),
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

func TestEditWithoutPriorReadZeroWidthOldRefusesLoud(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path":  path,
		"edits": hunks([2]string{"", "x"}),
	}))
	if err == nil || !strings.Contains(err.Error(), "zero-width") {
		t.Fatalf("a zero-width old is an args problem, unread file or not, got %v", err)
	}
}

func TestEditZeroWidthOldNamesTheHunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path":  path,
		"edits": hunks([2]string{"one", "1"}, [2]string{"", "x"}, [2]string{"two", "2"}),
	}))
	if err == nil || !strings.Contains(err.Error(), "hunk 2 of 3") || !strings.Contains(err.Error(), "zero-width") {
		t.Fatalf("a zero-width old must name its hunk, got %v", err)
	}
}

func TestEditWithoutPriorReadMissingFileRefusesLoud(t *testing.T) {
	ctx := core.WithSession(context.Background(), core.NewSession())
	_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path":  filepath.Join(t.TempDir(), "absent.txt"),
		"edits": hunks([2]string{"x", "y"}),
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
		"path":  path,
		"edits": hunks([2]string{"one", "two"}),
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
		"path":  path,
		"edits": hunks([2]string{"one", "two"}),
	})); err != nil {
		t.Fatalf("write mints the edit license: %v", err)
	}
}

func TestEditEmptyEditsListRefuses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.Edit().Exec(context.Background(), argsJSON(t, map[string]any{
		"path": path, "edits": []map[string]string{},
	}))
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("an empty edits list must refuse loud, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one" {
		t.Fatal("a refused call must not mutate the file")
	}
}

func TestEditHunkCountOverTheBoundRefuses(t *testing.T) {
	edits := make([]map[string]string, 33)
	for i := range edits {
		edits[i] = map[string]string{"old": "a", "new": "b"}
	}
	_, err := file.Edit().Exec(context.Background(), argsJSON(t, map[string]any{
		"path":  filepath.Join(t.TempDir(), "absent.txt"),
		"edits": edits,
	}))
	if err == nil || !strings.Contains(err.Error(), "33") || !strings.Contains(err.Error(), "32") {
		t.Fatalf("33 hunks must refuse naming the 32-hunk bound before the file is even read, got %v", err)
	}
}

func TestEditHunkTotalAtTheReadCeilingRefuses(t *testing.T) {
	_, err := file.Edit().Exec(context.Background(), argsJSON(t, map[string]any{
		"path":  filepath.Join(t.TempDir(), "absent.txt"),
		"edits": hunks([2]string{"a", strings.Repeat("x", readCap-1)}),
	}))
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(readCap)) {
		t.Fatalf("a hunk total at the read ceiling must refuse naming it, got %v", err)
	}
}

func TestEditHunkTotalUnderTheReadCeilingApplies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path":  path,
		"edits": hunks([2]string{"a", strings.Repeat("x", readCap-2)}),
	})); err != nil {
		t.Fatalf("a hunk total under the read ceiling applies: %v", err)
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
		"path":  path,
		"edits": hunks([2]string{"A content", "X"}),
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
		"path":  path,
		"edits": hunks([2]string{"one", "two"}),
	})); err != nil {
		t.Fatalf("edit: %v", err)
	}
	state, ok := session.Files[path]
	if !ok || state.Hash == "" {
		t.Fatal("edit must record fresh, complete provenance for subsequent edits")
	}
	if _, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path":  path,
		"edits": hunks([2]string{"two", "three"}),
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
		"path":  path,
		"edits": hunks([2]string{"beta", "beta2"}),
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
		"path":  path,
		"edits": hunks([2]string{"old", "new"}),
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
