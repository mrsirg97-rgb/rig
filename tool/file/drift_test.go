package file_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/tool/file"
)

func linesFile(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "line %02d\n", i)
	}
	return b.String()
}

func driftWindowSetup(t *testing.T, path, content string, offset, limit int) (context.Context, *core.Session) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	session := core.NewSession()
	ctx := core.WithSession(context.Background(), session)
	if _, err := file.NewRead().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "offset": offset, "limit": limit,
	})); err != nil {
		t.Fatalf("read: %v", err)
	}
	return ctx, session
}

func moveMtimeOnly(t *testing.T, path string) {
	t.Helper()
	moved := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, moved, moved); err != nil {
		t.Fatal(err)
	}
}

func TestDriftRefusalNamesIdenticalBytesWhenOnlyMtimeMoved(t *testing.T) {
	dir := realTemp(t)
	path := filepath.Join(dir, "code.txt")
	ctx, _ := driftSetup(t, path, "alpha\nbeta\ngamma\n")
	moveMtimeOnly(t, path)
	_, err := file.NewEdit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "beta", "new": "BETA",
	}))
	if err == nil || !strings.Contains(err.Error(), "the file changed since the read") {
		t.Fatalf("the drift must refuse: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "the bytes you read are unchanged") {
		t.Fatalf("identical content must say so, got:\n%s", msg)
	}
	if strings.Contains(msg, "--- as read") {
		t.Fatalf("identical content must not diff, got:\n%s", msg)
	}
}

func TestDriftRefusalAfterWindowedReadDiffsTheWindowNotTheFile(t *testing.T) {
	dir := realTemp(t)
	path := filepath.Join(dir, "code.txt")
	ctx, _ := driftWindowSetup(t, path, linesFile(30), 10, 4)
	var nw strings.Builder
	for i := 0; i < 30; i++ {
		if i == 12 {
			nw.WriteString("CHANGED\n")
			continue
		}
		fmt.Fprintf(&nw, "line %02d\n", i)
	}
	if err := os.WriteFile(path, []byte(nw.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.NewEdit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "line 11", "new": "eleven",
	}))
	if err == nil || !strings.Contains(err.Error(), "the file changed since the read") {
		t.Fatalf("the drift must refuse: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"-line 12", "+CHANGED", "line 10", "line 13"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal must diff the window the read returned, missing %q:\n%s", want, msg)
		}
	}
	for _, bad := range []string{"line 00", "line 29", "more lines"} {
		if strings.Contains(msg, bad) {
			t.Fatalf("the refusal must not drag the whole file in, found %q:\n%s", bad, msg)
		}
	}
}

func TestDriftRefusalAfterWindowedReadNamesIdenticalBytesWhenOnlyMtimeMoved(t *testing.T) {
	dir := realTemp(t)
	path := filepath.Join(dir, "code.txt")
	ctx, _ := driftWindowSetup(t, path, linesFile(30), 10, 4)
	moveMtimeOnly(t, path)
	_, err := file.NewEdit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "line 11", "new": "eleven",
	}))
	if err == nil || !strings.Contains(err.Error(), "the file changed since the read") {
		t.Fatalf("the drift must refuse: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "the bytes you read are unchanged") {
		t.Fatalf("an unchanged window must say so, got:\n%s", msg)
	}
	if strings.Contains(msg, "--- as read") || strings.Contains(msg, "more lines") {
		t.Fatalf("an unchanged window must not diff against the whole file, got:\n%s", msg)
	}
}

func TestDriftRefusalAfterWindowedReadStaysRefusedWhenTheWindowVanishes(t *testing.T) {
	dir := realTemp(t)
	path := filepath.Join(dir, "code.txt")
	ctx, _ := driftWindowSetup(t, path, linesFile(30), 10, 4)
	if err := os.WriteFile(path, []byte("short\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := file.NewEdit().Exec(ctx, argsJSON(t, map[string]any{
		"path": path, "old": "line 11", "new": "eleven",
	}))
	if err == nil || !strings.Contains(err.Error(), "the file changed since the read") {
		t.Fatalf("a window the file no longer has must still refuse: %v", err)
	}
}
