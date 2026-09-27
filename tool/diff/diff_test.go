package diff_test

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	difftool "github.com/mrsirg97-rgb/rig/tool/diff"
)

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
}

func git(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	for _, a := range [][]string{
		{"init"},
		{"config", "user.name", "rig test"},
		{"config", "user.email", "rig@test"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, e, err := git(t, dir, a...); err != nil {
			t.Fatalf("git %v: %v\n%s", a, e, e)
		}
	}
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	git(t, dir, "add", "-A")
	if _, e, err := git(t, dir, "commit", "--allow-empty", "-m", msg); err != nil {
		t.Fatalf("git commit: %v\n%s", e, e)
	}
}

func lastLine(s string) string {
	i := strings.LastIndexByte(s, '\n')
	if i < 0 {
		return s
	}
	return s[i+1:]
}

func TestFilesDefaultsToHeadNotTheIndex(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	initRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "base")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "f.txt")
	reply, err := difftool.Files(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("a staged edit must succeed: %v (%s)", err, reply)
	}
	if !strings.Contains(reply, "+two") {
		t.Fatalf("the empty ref must mean HEAD, not the index (a staged edit shows against HEAD):\n%s", reply)
	}
}

func TestFilesCleanTreeRepliesNoChanges(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	initRepo(t, dir)
	commitAll(t, dir, "base")
	reply, err := difftool.Files(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("a clean tree must succeed: %v (%s)", err, reply)
	}
	if reply != "no changes" {
		t.Fatalf("a clean tree must reply no changes, got %q", reply)
	}
}

func TestFilesDirtyTreeCappedAt100(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	initRepo(t, dir)
	var base, dirty strings.Builder
	for i := 1; i <= 150; i++ {
		fmt.Fprintf(&base, "line%d\n", i)
		fmt.Fprintf(&dirty, "CHG%d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(base.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "base")
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(dirty.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := git(t, dir, "diff", "--no-color", "-U3")
	if err != nil {
		t.Fatal(err)
	}
	gitLines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(gitLines) <= 100 {
		t.Fatalf("the fixture must exceed the cap, got %d lines", len(gitLines))
	}
	reply, err := difftool.Files(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("a dirty tree must succeed: %v", err)
	}
	want := strings.Join(gitLines[:99], "\n") + "\n… " + strconv.Itoa(len(gitLines)-99) + " more lines"
	if reply != want {
		t.Fatalf("capped body:\ngot %d lines, last = %q\nwant %d lines, last = %q",
			len(strings.Split(reply, "\n")), lastLine(reply), 100, lastLine(want))
	}
}

func TestFilesNonGitCwdRefusesLoud(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, err := difftool.Files(context.Background(), "", nil)
	if err == nil {
		t.Fatal("a non-git cwd must refuse")
	}
	want := "diff files: not a git repository (cwd " + dir + ")"
	if err.Error() != want {
		t.Fatalf("voice = %q, want %q", err.Error(), want)
	}
}

func TestFilesRefIsOneDotNotTwoDot(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	initRepo(t, dir)
	setFile := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setFile("aaa\n")
	commitAll(t, dir, "base")
	if _, e, err := git(t, dir, "tag", "base"); err != nil {
		t.Fatalf("tag: %v\n%s", e, e)
	}
	setFile("bbb\n")
	commitAll(t, dir, "head")
	setFile("ccc\n")

	reply, err := difftool.Files(context.Background(), "base", nil)
	if err != nil {
		t.Fatalf("a valid ref must succeed: %v (%s)", err, reply)
	}
	if !strings.Contains(reply, "-aaa") || !strings.Contains(reply, "+ccc") {
		t.Fatalf("the diff must be ref vs working tree (-aaa +ccc):\n%s", reply)
	}
	if strings.Contains(reply, "+bbb") {
		t.Fatalf("the diff must not be ref..HEAD (+bbb):\n%s", reply)
	}
}

func TestFilesPathsRestrictTheDiff(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	initRepo(t, dir)
	w := func(p, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, p), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("a.txt", "1\n")
	w("b.txt", "2\n")
	commitAll(t, dir, "base")
	w("a.txt", "A\n")
	w("b.txt", "B\n")
	reply, err := difftool.Files(context.Background(), "", []string{"a.txt"})
	if err != nil {
		t.Fatalf("paths must succeed: %v (%s)", err, reply)
	}
	if !strings.Contains(reply, "a.txt") {
		t.Fatalf("the named path must be in the diff:\n%s", reply)
	}
	if strings.Contains(reply, "b.txt") {
		t.Fatalf("the other path must be restricted out:\n%s", reply)
	}
}

func TestFilesGitFailurePassesTheStderrLine(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	initRepo(t, dir)
	commitAll(t, dir, "base")

	_, errb, err := git(t, dir, "diff", "--no-color", "-U3", "v9")
	if err == nil {
		t.Fatal("git must fail on the unknown ref")
	}
	first := strings.Split(strings.TrimSpace(errb), "\n")[0]
	_, terr := difftool.Files(context.Background(), "v9", nil)
	if terr == nil {
		t.Fatal("the unknown ref must refuse")
	}
	want := "diff files: " + first
	if terr.Error() != want {
		t.Fatalf("voice = %q, want %q", terr.Error(), want)
	}
}

func TestEngineHunksApplyToOldYieldNew(t *testing.T) {
	old := "alpha\nbravo\ncharlie\ndelta\necho\nfoxtrot\n"
	new := "alpha\nBRAVO\ncharlie\nGOLF\necho\nfoxtrot\n"
	got := difftool.Diff(old, new, "a", "b")
	if strings.TrimSpace(got) == "" {
		t.Fatal("the fixture pair differs: the engine must not reply empty")
	}
	checkApply(t, got, old, new)
}

func TestEngineHunksApplyOnRandomPairs(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	random := func() string {
		n := rng.Intn(8)
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteByte(byte('a' + rng.Intn(2)))
			if i+1 < n {
				b.WriteByte('\n')
			}
		}
		if n > 0 && rng.Intn(2) == 0 {
			b.WriteByte('\n')
		}
		return b.String()
	}
	for i := 0; i < 300; i++ {
		old, new := random(), random()
		if old == new {
			if got := difftool.Diff(old, new, "a", "b"); got != "" {
				t.Fatalf("identical pair %q: the engine must be empty, got %q", old, got)
			}
			continue
		}
		checkApply(t, difftool.Diff(old, new, "a", "b"), old, new)
	}
}

func checkApply(t *testing.T, patch, old, new string) {
	t.Helper()
	got, err := applyPatch(old, patch)
	if err != nil {
		t.Fatalf("the engine's hunks do not apply to the old string:\npatch:\n%s\nerr: %v", patch, err)
	}
	if got != new {
		t.Fatalf("applying the hunks to %q yields %q, want %q\npatch:\n%s", old, got, new, patch)
	}
}

func applyPatch(old, patch string) (string, error) {
	oldRecs := records(old)
	lines := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
	k := 0
	if k >= len(lines) || lines[k] == "" || !strings.HasPrefix(lines[k], "--- ") {
		return "", fmt.Errorf("the --- header is missing (first line %q)", lineOr(lines, k))
	}
	k++
	if k >= len(lines) || !strings.HasPrefix(lines[k], "+++ ") {
		return "", fmt.Errorf("the +++ header is missing (second line %q)", lineOr(lines, k))
	}
	k++
	var out []string
	trailing := "\n"
	oldPos := 0
	prevNew := false

	lastFromOldTail := false
	for k < len(lines) {
		h := lines[k]
		k++
		parts := strings.Split(strings.TrimPrefix(h, "@@ "), " ")
		if len(parts) != 3 || parts[2] != "@@" {
			return "", fmt.Errorf("malformed hunk header %q", h)
		}
		oStart, oCount, err := hunkSide(parts[0], "-")
		if err != nil {
			return "", err
		}
		nStart, nCount, err := hunkSide(parts[1], "+")
		if err != nil {
			return "", err
		}

		oldEnd := oStart
		if oCount > 0 {
			oldEnd--
		}
		if oldEnd < oldPos {
			return "", fmt.Errorf("header %q: the old range starts before the body's position %d", h, oldPos)
		}
		for oldPos < oldEnd {
			out = append(out, oldRecs[oldPos])
			oldPos++
			lastFromOldTail = oldPos == len(oldRecs)
		}
		wantNew := len(out)
		if nCount > 0 {
			wantNew++
		}
		if nStart != wantNew {
			return "", fmt.Errorf("header %q: the new range starts at %d, the body is at %d", h, nStart, wantNew)
		}
		nOld, nNew := 0, 0
		for k < len(lines) && !strings.HasPrefix(lines[k], "@@ ") {
			l := lines[k]
			if l == "\\ No newline at end of file" {
				if prevNew {
					trailing = ""
				}
				prevNew = false
				k++
				continue
			}
			if len(l) == 0 {
				return "", fmt.Errorf("an empty body line ends the hunk without a header")
			}
			switch l[0] {
			case ' ', '-':
				if oldPos >= len(oldRecs) {
					return "", fmt.Errorf("%c-line %q: the old string has no record %d", l[0], l, oldPos+1)
				}
				if oldRecs[oldPos] != l[1:] {
					return "", fmt.Errorf("%c-line %q does not match the old record %q at position %d", l[0], l, oldRecs[oldPos], oldPos+1)
				}
				oldPos++
				nOld++
				if l[0] == ' ' {
					out = append(out, l[1:])
					nNew++
					lastFromOldTail = false
				}
			case '+':
				out = append(out, l[1:])
				nNew++
				lastFromOldTail = false
			default:
				return "", fmt.Errorf("body line %q is not a context, delete, or insert line", l)
			}
			prevNew = l[0] != '-'
			k++
		}
		if oCount != nOld {
			return "", fmt.Errorf("header %q: the old side says %d lines, the body holds %d", h, oCount, nOld)
		}
		if nCount != nNew {
			return "", fmt.Errorf("header %q: the new side says %d lines, the body holds %d", h, nCount, nNew)
		}
	}

	for oldPos < len(oldRecs) {
		out = append(out, oldRecs[oldPos])
		oldPos++
		lastFromOldTail = oldPos == len(oldRecs)
	}
	if len(out) == 0 {
		return "", nil
	}
	if trailing == "\n" && lastFromOldTail && !strings.HasSuffix(old, "\n") {
		trailing = ""
	}
	return strings.Join(out, "\n") + trailing, nil
}

func hunkSide(s, sign string) (start, count int, err error) {
	if len(s) == 0 || len(sign) == 0 || s[0] != sign[0] {
		return 0, 0, fmt.Errorf("hunk side %q is not signed %q", s, sign)
	}
	body := s[1:]
	if i := strings.IndexByte(body, ','); i >= 0 {
		if start, err = strconv.Atoi(body[:i]); err != nil {
			return 0, 0, fmt.Errorf("bad hunk side %q", s)
		}
		if count, err = strconv.Atoi(body[i+1:]); err != nil {
			return 0, 0, fmt.Errorf("bad hunk side %q", s)
		}
		return start, count, nil
	}
	if start, err = strconv.Atoi(body); err != nil {
		return 0, 0, fmt.Errorf("bad hunk side %q", s)
	}
	return start, 1, nil
}

func records(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func lineOr(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<eof>"
}
