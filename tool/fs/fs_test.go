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

func execCtx(t *testing.T, tool core.Tool, ctx context.Context, args string) (string, error) {
	t.Helper()
	return tool.Exec(ctx, json.RawMessage(args))
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

func TestFindBarePatternsReachNestedFiles(t *testing.T) {
	root := t.TempDir()
	mk(t, root, map[string]string{"a/b/c.txt": "x", "top.txt": "y"}, nil)
	bare, err := exec(t, fs.Find(), fmt.Sprintf(`{"pattern":"*.txt","root":%q}`, root))
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !strings.Contains(bare, "top.txt") || !strings.Contains(bare, "a/b/c.txt") {
		t.Errorf("bare pattern *.txt must reach nested files by name:\n%s", bare)
	}
	nested, err := exec(t, fs.Find(), fmt.Sprintf(`{"pattern":"**/c.txt","root":%q}`, root))
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if nested != "a/b/c.txt" {
		t.Errorf("find **/c.txt = %q, want %q", nested, "a/b/c.txt")
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

func TestGrepSkipsAndNamesUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses mode bits")
	}
	root := t.TempDir()
	mk(t, root, map[string]string{"a.txt": "needle\n", "sealed/inner.txt": "needle\n"}, nil)
	sealed := filepath.Join(root, "sealed")
	if err := os.Chmod(sealed, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(sealed, 0o755)
	content, err := exec(t, fs.Grep(), fmt.Sprintf(`{"pattern":"needle","root":%q}`, root))
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(content, "a.txt:1: needle") {
		t.Errorf("readable match not reported:\n%s", content)
	}
	if strings.Contains(content, "sealed/") {
		t.Errorf("unreadable subtree not skipped:\n%s", content)
	}
	if !strings.Contains(content, "[skipped: 1 unreadable]") {
		t.Errorf("skip not named:\n%s", content)
	}
}

func TestFindSkipsAndNamesUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses mode bits")
	}
	root := t.TempDir()
	mk(t, root, map[string]string{"a.txt": "x", "sealed/inner.txt": "x"}, nil)
	sealed := filepath.Join(root, "sealed")
	if err := os.Chmod(sealed, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(sealed, 0o755)
	content, err := exec(t, fs.Find(), fmt.Sprintf(`{"pattern":"**","root":%q}`, root))
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !strings.Contains(content, "a.txt") {
		t.Errorf("readable match not reported:\n%s", content)
	}
	if strings.Contains(content, "sealed/") {
		t.Errorf("unreadable subtree not skipped:\n%s", content)
	}
	if !strings.Contains(content, "[skipped: 1 unreadable]") {
		t.Errorf("skip not named:\n%s", content)
	}
}

func TestGrepCapsMatchedLineBytes(t *testing.T) {
	root := t.TempDir()
	mk(t, root, map[string]string{"big.txt": "head\n" + strings.Repeat("x", 5000) + "\n"}, nil)
	content, err := exec(t, fs.Grep(), fmt.Sprintf(`{"pattern":"x","root":%q}`, root))
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(content, "[line truncated]") {
		t.Errorf("line cap not named:\n%q", tail(content))
	}
	first := strings.SplitN(content, "\n", 2)[0]
	if got := len(first); got > len("big.txt:2: ")+512+len(" [line truncated]") {
		t.Errorf("matched line = %d bytes, want bounded", got)
	}
}

func TestExecutionsRespectContextCancellation(t *testing.T) {
	root := t.TempDir()
	mk(t, root, map[string]string{"a.txt": "x"}, nil)
	for _, tc := range []struct {
		tool core.Tool
		args string
	}{
		{fs.Find(), fmt.Sprintf(`{"pattern":"**","root":%q}`, root)},
		{fs.Grep(), fmt.Sprintf(`{"pattern":"x","root":%q}`, root)},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := execCtx(t, tc.tool, ctx, tc.args); err == nil {
			t.Errorf("%s: cancelled context not surfaced", tc.tool.Name())
		}
	}
}

func tail(s string) string {
	if len(s) > 200 {
		return "..." + s[len(s)-200:]
	}
	return s
}
