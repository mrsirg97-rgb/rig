package fs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/looper/core"
	"github.com/mrsirg97-rgb/looper/tool/fs"
)

func exec(t *testing.T, tool core.Tool, args string) (string, error) {
	t.Helper()
	return tool.Exec(context.Background(), json.RawMessage(args))
}

func mk(t *testing.T, root string, files map[string]string, dirs []string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLSRendersKindNameAndSize(t *testing.T) {
	root := t.TempDir()
	mk(t, root, map[string]string{"notes.txt": "hello world\n"}, []string{"sub"})
	content, err := exec(t, fs.LS(), fmt.Sprintf(`{"path":%q}`, root))
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if !strings.Contains(content, "d sub") {
		t.Errorf("want directory line %q in output:\n%s", "d sub", content)
	}
	if !strings.Contains(content, "f notes.txt\t12") {
		t.Errorf("want %q in output:\n%s", "f notes.txt\t12", content)
	}
}

func TestLSHidesDotEntriesUnlessAsked(t *testing.T) {
	root := t.TempDir()
	mk(t, root, map[string]string{".hidden": "x", "open.txt": "y"}, nil)
	denied, err := exec(t, fs.LS(), fmt.Sprintf(`{"path":%q}`, root))
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if strings.Contains(denied, ".hidden") {
		t.Errorf("dot-entry visible without asking:\n%s", denied)
	}
	asked, err := exec(t, fs.LS(), fmt.Sprintf(`{"path":%q,"hidden":true}`, root))
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if !strings.Contains(asked, "f .hidden\t1") {
		t.Errorf("dot-entry absent with hidden=true:\n%s", asked)
	}
}

func TestLSNamesItsTruncation(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 1001; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%04d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	content, err := exec(t, fs.LS(), fmt.Sprintf(`{"path":%q}`, root))
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if !strings.Contains(content, "[truncated: 1000 of 1001]") {
		t.Errorf("truncation not named:\n%q", tail(content))
	}
	if got := len(strings.Split(content, "\n")); got != 1001 {
		t.Errorf("lines = %d, want %d", got, 1001)
	}
}

func TestFindMatchesNestedGlobs(t *testing.T) {
	root := t.TempDir()
	mk(t, root, map[string]string{"a/b/c.txt": "x", "top.txt": "y"}, nil)
	nested, err := exec(t, fs.Find(), fmt.Sprintf(`{"pattern":"**/c.txt","root":%q}`, root))
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if nested != "a/b/c.txt" {
		t.Errorf("find **/c.txt = %q, want %q", nested, "a/b/c.txt")
	}
	toplevel, err := exec(t, fs.Find(), fmt.Sprintf(`{"pattern":"*.txt","root":%q}`, root))
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if toplevel != "top.txt" {
		t.Errorf("find *.txt = %q, want %q", toplevel, "top.txt")
	}
	span, err := exec(t, fs.Find(), fmt.Sprintf(`{"pattern":"a/**","root":%q}`, root))
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if span != "a/b/c.txt" {
		t.Errorf("find a/** = %q, want %q", span, "a/b/c.txt")
	}
}

func TestFindRefusesMissingPattern(t *testing.T) {
	_, err := exec(t, fs.Find(), `{"root":"."}`)
	if err == nil || !strings.Contains(err.Error(), "pattern required") {
		t.Errorf("err = %v, want a loud refusal naming the missing pattern", err)
	}
}

func TestGrepPrintsPathLineText(t *testing.T) {
	root := t.TempDir()
	mk(t, root, map[string]string{"a.txt": "keep\nneedle here\nkeep\n"}, nil)
	content, err := exec(t, fs.Grep(), fmt.Sprintf(`{"pattern":"needle","root":%q}`, root))
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if content != "a.txt:2: needle here" {
		t.Errorf("grep = %q, want %q", content, "a.txt:2: needle here")
	}
}

func TestGrepGlobFilterRestricts(t *testing.T) {
	root := t.TempDir()
	mk(t, root, map[string]string{"x.txt": "needle\n", "y.md": "needle\n"}, nil)
	restricted, err := exec(t, fs.Grep(), fmt.Sprintf(`{"pattern":"needle","root":%q,"glob":"*.txt"}`, root))
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(restricted, "x.txt:1: needle") || strings.Contains(restricted, "y.md") {
		t.Errorf("glob filter not applied:\n%s", restricted)
	}
}

func TestGrepSkipsGitAndBinary(t *testing.T) {
	root := t.TempDir()
	mk(t, root, map[string]string{
		".git/objects/x.txt": "needle\n",
		"bin.dat":            "abc\x00needle\n",
	}, nil)
	content, err := exec(t, fs.Grep(), fmt.Sprintf(`{"pattern":"needle","root":%q}`, root))
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if content != "(no matches)" {
		t.Errorf("grep = %q, want (no matches): .git and binary skips", content)
	}
}

func TestGrepNamesItsTruncationWithTrueTotals(t *testing.T) {
	root := t.TempDir()
	mk(t, root, map[string]string{"hot.txt": strings.Repeat("x\n", 600)}, nil)
	content, err := exec(t, fs.Grep(), fmt.Sprintf(`{"pattern":"x","root":%q}`, root))
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(content, "[truncated: 500 of 600]") {
		t.Errorf("truncation not named with true totals:\n%q", tail(content))
	}
	if got := len(strings.Split(content, "\n")); got != 501 {
		t.Errorf("lines = %d, want %d (500 matches + marker)", got, 501)
	}
}

func TestDescriptionsArePresent(t *testing.T) {
	for _, tool := range []core.Tool{fs.LS(), fs.Find(), fs.Grep()} {
		if tool.Name() == "" || tool.Description() == "" || len(tool.Schema()) == 0 {
			t.Errorf("tool %q missing name, description, or schema", tool.Name())
		}
	}
}

func tail(s string) string {
	if len(s) > 200 {
		return "..." + s[len(s)-200:]
	}
	return s
}
