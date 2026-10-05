package file_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/tool/file"
)

func TestReadReadAndExecShareTheirChecks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lines.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := file.NewRead()
	ctx := context.Background()
	negative := -1

	fromRead, readErr := tool.Read(ctx, path, &negative, nil, false)
	_, execErr := tool.Exec(ctx, argsJSON(t, map[string]any{"path": path, "offset": -1}))
	if readErr == nil || execErr == nil {
		t.Fatalf("a negative offset refuses on both doors, read=%v exec=%v", readErr, execErr)
	}
	if readErr.Error() != "read: offset -1 is negative" || execErr.Error() != readErr.Error() {
		t.Fatalf("the refusal is the same words on either door: read %q exec %q", readErr, execErr)
	}
	if fromRead != "" {
		t.Fatalf("a refused read carries no content, got %q", fromRead)
	}

	past := 5
	_, readErr = tool.Read(ctx, path, &past, nil, false)
	_, execErr = tool.Exec(ctx, argsJSON(t, map[string]any{"path": path, "offset": past}))
	if readErr == nil || execErr == nil || readErr.Error() != execErr.Error() {
		t.Fatalf("an offset past the end refuses the same way on either door: read=%v exec=%v", readErr, execErr)
	}

	got, err := tool.Read(ctx, path, nil, nil, false)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want, err := tool.Exec(ctx, argsJSON(t, map[string]any{"path": path}))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got != want {
		t.Fatalf("one read replies the same bytes through either door: read %q exec %q", got, want)
	}

	one := 1
	limited, err := tool.Read(ctx, path, nil, &one, false)
	if err != nil {
		t.Fatalf("a bounded read: %v", err)
	}
	bounded, err := tool.Exec(ctx, argsJSON(t, map[string]any{"path": path, "limit": 1}))
	if err != nil {
		t.Fatalf("a bounded exec: %v", err)
	}
	if limited != bounded {
		t.Fatalf("the limit is the verb's own bound, read %q exec %q", limited, bounded)
	}
	if strings.Contains(limited, "two") {
		t.Fatalf("a read of one line stops at the line the verb was given, got %q", limited)
	}
}

func TestWriteWriteAndExecShareTheirChecks(t *testing.T) {
	dir := t.TempDir()
	tool := file.NewWrite()
	ctx := context.Background()

	got, err := tool.Write(ctx, filepath.Join(dir, "a.txt"), "hello")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	want, err := tool.Exec(ctx, argsJSON(t, map[string]any{"path": filepath.Join(dir, "a.txt"), "content": "hello"}))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got != want {
		t.Fatalf("one write replies the same bytes through either door: write %q exec %q", got, want)
	}

	_, err = tool.Write(ctx, dir, "hello")
	_, execErr := tool.Exec(ctx, argsJSON(t, map[string]any{"path": dir, "content": "hello"}))
	if err == nil || execErr == nil || err.Error() != execErr.Error() {
		t.Fatalf("a write to a directory refuses the same way on either door: write=%v exec=%v", err, execErr)
	}
}

func TestEditEditAndExecShareTheirChecks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\none\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := file.NewEdit()
	ctx := context.Background()

	_, err := tool.Edit(ctx, path, "", "three")
	_, execErr := tool.Exec(ctx, argsJSON(t, map[string]any{"path": path, "old": "", "new": "three"}))
	if err == nil || execErr == nil {
		t.Fatalf("an empty old refuses on both doors, edit=%v exec=%v", err, execErr)
	}
	if err.Error() != "edit: old is empty; give the text to replace" || execErr.Error() != err.Error() {
		t.Fatalf("the refusal is the same words on either door: edit %q exec %q", err, execErr)
	}

	_, err = tool.Edit(ctx, path, "one", "three")
	_, execErr = tool.Exec(ctx, argsJSON(t, map[string]any{"path": path, "old": "one", "new": "three"}))
	if err == nil || execErr == nil || err.Error() != execErr.Error() {
		t.Fatalf("an old that matches twice refuses the same way on either door: edit=%v exec=%v", err, execErr)
	}

	got, err := tool.Edit(ctx, path, "two", "three")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if err := os.WriteFile(path, []byte("one\ntwo\none\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := tool.Exec(ctx, argsJSON(t, map[string]any{"path": path, "old": "two", "new": "three"}))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got != want {
		t.Fatalf("the same edit replies the same bytes through either door: edit %q exec %q", got, want)
	}
	if !strings.HasPrefix(got, "edited "+path+": replaced 3 byte(s)") {
		t.Fatalf("the reply names the path and the bytes replaced: %q", got)
	}
}
