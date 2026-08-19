package diff_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state"
	difftool "github.com/mrsirg97-rgb/rig/tool/diff"
)

// --- helpers ---

func openState(t *testing.T) store.DB {
	t.Helper()
	db, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return db
}

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

// world is one session's state rows: the last verb's fixtures.
type world struct {
	db  store.DB
	sid string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{db: openState(t), sid: "w"}
	if e := state.RecordSession(context.Background(), w.db, w.sid, "/w", "m", "v"); e != nil {
		t.Fatal(e)
	}
	return w
}

// call lands one completed observation and returns its message seq.
func (w *world) call(t *testing.T, name, args, result string, failure *string) int64 {
	t.Helper()
	ctx := context.Background()
	seq, e := state.RecordMessage(ctx, w.db, w.sid, "assistant", "", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	id := "c" + strconv.FormatInt(seq, 10)
	if e := state.RecordToolCall(ctx, w.db, seq, id, name, args); e != nil {
		t.Fatalf("a decodable args string must land: %v", e)
	}
	if result != "" {
		if e := state.RecordToolResult(ctx, w.db, id, result, failure); e != nil {
			t.Fatal(e)
		}
	}
	return seq
}

func (w *world) exec(t *testing.T, args string) (string, error) {
	t.Helper()
	sess := core.NewSession()
	sess.ID = w.sid
	tool := difftool.New(w.db)
	return tool.Exec(core.WithSession(context.Background(), sess), json.RawMessage(args))
}

func itob(n int64) string { return strconv.FormatInt(n, 10) }

func lastLine(s string) string {
	i := strings.LastIndexByte(s, '\n')
	if i < 0 {
		return s
	}
	return s[i+1:]
}

// --- the named cases (SPEC_DIFF, PR B: the tool) ---

// files: a clean tree replies identical.
func TestFilesCleanTreeRepliesIdentical(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	initRepo(t, dir)
	commitAll(t, dir, "base")
	tool := difftool.New(openState(t))
	reply, err := tool.Exec(context.Background(), json.RawMessage(`{"mode":"files"}`))
	if err != nil {
		t.Fatalf("a clean tree must succeed: %v (%s)", err, reply)
	}
	if reply != "identical" {
		t.Fatalf("a clean tree must reply identical, got %q", reply)
	}
}

// files: a dirty tree's body is capped at 100 lines, the
// "… K more lines" marker exact, K counting the elided lines.
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
	// the ground truth: git's own output (the test may shell out).
	out, _, err := git(t, dir, "diff", "--no-color", "-U3")
	if err != nil {
		t.Fatal(err)
	}
	gitLines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(gitLines) <= 100 {
		t.Fatalf("the fixture must exceed the cap, got %d lines", len(gitLines))
	}
	tool := difftool.New(openState(t))
	reply, err := tool.Exec(context.Background(), json.RawMessage(`{"mode":"files"}`))
	if err != nil {
		t.Fatalf("a dirty tree must succeed: %v", err)
	}
	want := strings.Join(gitLines[:99], "\n") + "\n… " + strconv.Itoa(len(gitLines)-99) + " more lines"
	if reply != want {
		t.Fatalf("capped body:\ngot %d lines, last = %q\nwant %d lines, last = %q",
			len(strings.Split(reply, "\n")), lastLine(reply), 100, lastLine(want))
	}
}

// files: a non-git cwd refuses loud, naming the reason (the cwd in the
// voice).
func TestFilesNonGitCwdRefusesLoud(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	tool := difftool.New(openState(t))
	_, err := tool.Exec(context.Background(), json.RawMessage(`{"mode":"files"}`))
	if err == nil {
		t.Fatal("a non-git cwd must refuse")
	}
	want := "diff files: not a git repository (cwd " + dir + ")"
	if err.Error() != want {
		t.Fatalf("voice = %q, want %q", err.Error(), want)
	}
}

// files: a ref is honored — the one-dot form `git diff <ref>` (ref vs
// working tree), not `ref..HEAD`.
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
	setFile("ccc\n") // the working tree: distinct from ref and HEAD

	tool := difftool.New(openState(t))
	reply, err := tool.Exec(context.Background(), json.RawMessage(`{"mode":"files","ref":"base"}`))
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

// files: paths are honored (the diff is restricted to them).
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
	tool := difftool.New(openState(t))
	reply, err := tool.Exec(context.Background(), json.RawMessage(`{"mode":"files","paths":["a.txt"]}`))
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

// files: a git failure (an unknown ref) passes the stderr line through,
// prefixed.
func TestFilesGitFailurePassesTheStderrLine(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	initRepo(t, dir)
	commitAll(t, dir, "base")
	// the ground truth: git's own first stderr line (the test may shell
	// out).
	_, errb, err := git(t, dir, "diff", "--no-color", "-U3", "v9")
	if err == nil {
		t.Fatal("git must fail on the unknown ref")
	}
	first := strings.Split(strings.TrimSpace(errb), "\n")[0]
	tool := difftool.New(openState(t))
	_, terr := tool.Exec(context.Background(), json.RawMessage(`{"mode":"files","ref":"v9"}`))
	if terr == nil {
		t.Fatal("the unknown ref must refuse")
	}
	want := "diff files: " + first
	if terr.Error() != want {
		t.Fatalf("voice = %q, want %q", terr.Error(), want)
	}
}

// last: two calls with the same (tool, args) diff newest against
// previous, and the header names both observations.
func TestLastDiffsNewestAgainstPreviousHeaderNamesBoth(t *testing.T) {
	w := newWorld(t)
	w.call(t, "bash", `{"command":"ls"}`, "context line\nold line\n", nil)
	w.call(t, "bash", `{"command":"ls"}`, "context line\nnew line\n", nil)
	reply, err := w.exec(t, `{"mode":"last","tool":"bash","args":{"command":"ls"}}`)
	if err != nil {
		t.Fatalf("a pair must succeed: %v (%s)", err, reply)
	}
	rows, err := state.RecentToolCalls(context.Background(), w.db, w.sid, "bash", `{"command":"ls"}`, 1)
	if err != nil || len(rows) != 2 {
		t.Fatalf("fixture rows = %d, want 2: %v", len(rows), err)
	}
	nt, ot := rows[0].StartedAt.Format(time.RFC3339Nano), rows[1].StartedAt.Format(time.RFC3339Nano)
	want := fmt.Sprintf(`diff last bash {"command":"ls"} · old %s seq %d · new %s seq %d

--- %s
+++ %s
@@ -1,2 +1,2 @@
 context line
-old line
+new line`, ot, rows[1].Seq, nt, rows[0].Seq, ot, nt)
	if reply != want {
		t.Fatalf("reply = %q, want %q", reply, want)
	}
}

// last: an identical pair replies identical.
func TestLastIdenticalPairRepliesIdentical(t *testing.T) {
	w := newWorld(t)
	w.call(t, "bash", `{"command":"ls"}`, "same\n", nil)
	w.call(t, "bash", `{"command":"ls"}`, "same\n", nil)
	reply, err := w.exec(t, `{"mode":"last","tool":"bash","args":{"command":"ls"}}`)
	if err != nil {
		t.Fatalf("an identical pair must succeed: %v", err)
	}
	if reply != "identical" {
		t.Fatalf("an identical pair must reply identical, got %q", reply)
	}
}

// last: exactly one matching call replies no earlier observation.
func TestLastSingleCallRepliesNoEarlier(t *testing.T) {
	w := newWorld(t)
	w.call(t, "bash", `{"command":"ls"}`, "one\n", nil)
	reply, err := w.exec(t, `{"mode":"last","tool":"bash","args":{"command":"ls"}}`)
	if err != nil {
		t.Fatalf("one call must be a named reply, not a refusal: %v", err)
	}
	if reply != "no earlier observation" {
		t.Fatalf("one call must reply no earlier observation, got %q", reply)
	}
}

// last: zero matching calls replies no earlier observation.
func TestLastZeroCallsRepliesNoEarlier(t *testing.T) {
	w := newWorld(t)
	w.call(t, "bash", `{"command":"ls"}`, "one\n", nil)
	reply, err := w.exec(t, `{"mode":"last","tool":"read","args":{"path":"/x"}}`)
	if err != nil {
		t.Fatalf("zero calls must be a named reply, not a refusal: %v", err)
	}
	if reply != "no earlier observation" {
		t.Fatalf("zero calls must reply no earlier observation, got %q", reply)
	}
}

// last: n=2 picks the second-previous row; an n beyond what is
// available replies no earlier observation.
func TestLastNPicksNthPreviousAndBeyondRefuses(t *testing.T) {
	w := newWorld(t)
	seq1 := w.call(t, "bash", `{"command":"ls"}`, "one\n", nil)
	w.call(t, "bash", `{"command":"ls"}`, "two\n", nil)
	w.call(t, "bash", `{"command":"ls"}`, "three\n", nil)
	reply, err := w.exec(t, `{"mode":"last","tool":"bash","args":{"command":"ls"},"n":2}`)
	if err != nil {
		t.Fatalf("n=2 must succeed: %v (%s)", err, reply)
	}
	if !strings.Contains(reply, "-one") || !strings.Contains(reply, "+three") {
		t.Fatalf("n=2 must diff newest against second-previous (one vs three):\n%s", reply)
	}
	if !strings.Contains(reply, "seq "+itob(seq1)) {
		t.Fatalf("the header must name the second-previous observation (seq %d):\n%s", seq1, reply)
	}
	if strings.Contains(reply, "\ntwo") {
		t.Fatalf("the middle observation must not enter the reply:\n%s", reply)
	}
	reply, err = w.exec(t, `{"mode":"last","tool":"bash","args":{"command":"ls"},"n":4}`)
	if err != nil {
		t.Fatalf("an n beyond the available must be a named reply, not a refusal: %v", err)
	}
	if reply != "no earlier observation" {
		t.Fatalf("n=4 with 3 rows must reply no earlier observation, got %q", reply)
	}
}

// last: key order and whitespace in the query args do not matter; the
// gripe's case named: a bash call with the same command IS the same
// observation.
func TestLastQueryArgsKeyOrderWhitespaceIgnored(t *testing.T) {
	w := newWorld(t)
	w.call(t, "bash", `{"command":"ls","cwd":"/x"}`, "r1\n", nil)
	w.call(t, "bash", `{"command":"ls","cwd":"/x"}`, "r2\n", nil)
	reply, err := w.exec(t, `{"mode":"last","tool":"bash","args":{"cwd": "/x", "command" : "ls"}}`)
	if err != nil {
		t.Fatalf("the same call, retyped with order and whitespace, must match: %v (%s)", err, reply)
	}
	if reply == "no earlier observation" || reply == "identical" {
		t.Fatalf("the pair must be found, got %q", reply)
	}
	// the header carries the canonical form of the query's args.
	if !strings.HasPrefix(reply, `diff last bash {"command":"ls","cwd":"/x"} · old `) {
		t.Fatalf("the header must name the canonical args:\n%s", reply)
	}
}

// last: a value changed (ls vs ls -la) is a different observation: no
// match.
func TestLastValueChangedIsDifferentObservation(t *testing.T) {
	w := newWorld(t)
	w.call(t, "bash", `{"command":"ls"}`, "r1\n", nil)
	w.call(t, "bash", `{"command":"ls"}`, "r2\n", nil)
	reply, err := w.exec(t, `{"mode":"last","tool":"bash","args":{"command":"ls -la"}}`)
	if err != nil {
		t.Fatalf("a different observation must be a named reply, not a refusal: %v", err)
	}
	if reply != "no earlier observation" {
		t.Fatalf("ls -la is not ls: %q", reply)
	}
}

// last: a failed call (err set, result set) still participates.
func TestLastFailedCallParticipates(t *testing.T) {
	w := newWorld(t)
	failure := "exit 1"
	w.call(t, "bash", `{"command":"false"}`, "err-out-1\n", &failure)
	w.call(t, "bash", `{"command":"false"}`, "err-out-2\n", nil)
	reply, err := w.exec(t, `{"mode":"last","tool":"bash","args":{"command":"false"}}`)
	if err != nil {
		t.Fatalf("a failed call must participate: %v (%s)", err, reply)
	}
	if !strings.Contains(reply, "-err-out-1") || !strings.Contains(reply, "+err-out-2") {
		t.Fatalf("the failed call's result must be in the diff:\n%s", reply)
	}
}

// last: no session in ctx is a loud refusal, not a global scan.
func TestLastNoSessionIsALoudRefusal(t *testing.T) {
	w := newWorld(t)
	w.call(t, "bash", `{"command":"ls"}`, "r1\n", nil)
	w.call(t, "bash", `{"command":"ls"}`, "r2\n", nil)
	tool := difftool.New(w.db)
	_, err := tool.Exec(context.Background(), json.RawMessage(`{"mode":"last","tool":"bash","args":{"command":"ls"}}`))
	if err == nil {
		t.Fatal("no session in ctx must refuse")
	}
	want := "diff last: no session in context (the loop threads one)"
	if err.Error() != want {
		t.Fatalf("voice = %q, want %q", err.Error(), want)
	}
}

// last: a re-landed tail after compaction (fresh seqs, verbatim
// name/args/result) diffs as an ordinary row — and, being the newest
// copy, it is the observation the pair finds.
func TestLastRelandedTailDiffsAsOrdinaryRow(t *testing.T) {
	w := newWorld(t)
	sess := core.NewSession()
	sess.ID = w.sid
	rec := state.NewRecorder(&nullFrontend{}, w.db, "/w", "m", "v", w.sid, sess)

	// the turn: one completed observation, as the loop records it.
	seq1 := w.call(t, "bash", `{"command":"ls"}`, "r1\n", nil)
	id := "c" + itob(seq1)
	// the loop's session carries the transcript (the recorder reads it
	// at compaction).
	sess.Append(core.Message{Role: core.RoleUser, Content: "go"})
	sess.Append(core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: id, Name: "bash", Args: json.RawMessage(`{"command":"ls"}`)}}})
	sess.Append(core.Message{Role: core.RoleTool, ToolID: id, Content: "r1\n"})

	rec.Notify(core.Compacted{Summary: "[compaction] the summary"})

	// the re-landed row is a fresh copy: same name/args/result, a fresh
	// seq. It is a completed observation now.
	rows, err := state.RecentToolCalls(context.Background(), w.db, w.sid, "bash", `{"command":"ls"}`, 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (the tail's copy): %v", len(rows), err)
	}
	if rows[0].Result != "r1\n" {
		t.Fatalf("the re-landed result = %q, want r1\\n verbatim", rows[0].Result)
	}
	relandedSeq := rows[0].Seq
	if relandedSeq <= seq1 {
		t.Fatalf("the re-landed row's seq = %d, want a fresh seq past the original's %d", relandedSeq, seq1)
	}

	// a new observation after the compaction: the pair is the new row
	// against the re-landed copy (the newest of the pair's kind).
	w.call(t, "bash", `{"command":"ls"}`, "r2\n", nil)
	reply, err := w.exec(t, `{"mode":"last","tool":"bash","args":{"command":"ls"}}`)
	if err != nil {
		t.Fatalf("the re-landed pair must diff: %v (%s)", err, reply)
	}
	if !strings.Contains(reply, "-r1") || !strings.Contains(reply, "+r2") {
		t.Fatalf("the re-landed row must be in the diff:\n%s", reply)
	}
	// the header names the old observation; it must be the fresh row.
	if !strings.Contains(reply, "seq "+itob(relandedSeq)+" · new") {
		t.Fatalf("the old observation must be the re-landed row (seq %d), not the original (seq %d):\n%s", relandedSeq, seq1, reply)
	}
}

// last: the spurious-identical guard (SPEC_DIFF PR B, named; decision
// 5): one call, a compaction, no call after. The current world has
// exactly one observation (the re-landed copy), so the reply is
// `no earlier observation`. Without the world boundary the copy would
// pair against its own original and reply the spurious `identical`.
func TestLastRelandedCopyNeverRepliesSpuriousIdentical(t *testing.T) {
	w := newWorld(t)
	seq1 := w.call(t, "bash", `{"command":"ls"}`, "same\n", nil)
	id := "c" + itob(seq1)
	sess := core.NewSession()
	sess.ID = w.sid
	sess.Append(core.Message{Role: core.RoleUser, Content: "[compaction] the summary"})
	sess.Append(core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: id, Name: "bash", Args: json.RawMessage(`{"command":"ls"}`)}}})
	sess.Append(core.Message{Role: core.RoleTool, ToolID: id, Content: "same\n"})
	rec := state.NewRecorder(&nullFrontend{}, w.db, "/w", "m", "v", w.sid, sess)
	rec.Notify(core.Compacted{Summary: "[compaction] the summary"})

	reply, err := w.exec(t, `{"mode":"last","tool":"bash","args":{"command":"ls"}}`)
	if err != nil {
		t.Fatalf("one observation must be a named reply, not a refusal: %v (%s)", err, reply)
	}
	if reply != "no earlier observation" {
		t.Fatalf("the copy has no earlier observation in the current world: got %q, want no earlier observation (the spurious identical is the copy pairing against its own original)", reply)
	}
}

// last: the engine's hunks are a patch (SPEC_DIFF testing): applying
// them to the old string yields the new string, and the hunk headers'
// ranges are consistent with the body. Two correct diffs may pick
// different equal-cost scripts, so the property is the contract — not
// git's bytes (chasing them is chasing xdiff's C).
func TestEngineHunksApplyToOldYieldNew(t *testing.T) {
	old := "alpha\nbravo\ncharlie\ndelta\necho\nfoxtrot\n"
	new := "alpha\nBRAVO\ncharlie\nGOLF\necho\nfoxtrot\n"
	got := difftool.Diff(old, new, "a", "b")
	if strings.TrimSpace(got) == "" {
		t.Fatal("the fixture pair differs: the engine must not reply empty")
	}
	checkApply(t, got, old, new)
}

// the engine over random pairs, seeded: the property holds on ties
// included (the alphabet is two letters; the files are short).
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

// checkApply asserts the engine's promise: the hunks applied to old
// yield new byte-for-byte. The test's patch-apply helper (SPEC_DIFF
// testing): the test may apply; the runtime may not.
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

// applyPatch parses the engine's layout and applies it to old, verifying
// the hunk headers' ranges against the body as it goes: the context and
// deleted records must match old at the header's position, the new
// position must match what the body has built so far, and the header
// counts must match the body's line counts. A record missing its
// trailing newline is part of the record (the no-newline marker says so
// for the side it belongs to).
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
	// lastFromOldTail marks that out's last record is old's last record,
	// carried over: its line-ending state is then old's, exactly as
	// git apply consults the source file for records outside the patch.
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
		// old records the hunk leaves behind carry over unchanged. A
		// non-empty old range names its first (1-based) line; a zero-count
		// range names the insertion point (the 0-based index).
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
	// old records past the last hunk carry over unchanged.
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

// hunkSide parses one side of a @@ header: "1,3", "1" (a single line),
// or "0,0" (the insertion point). The sign is the side's.
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

// records mirrors the engine's split: a trailing newline is not a
// record, and an empty string has none.
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

// --- the wire: name, description and schema, verbatim from the spec ---

func TestNameDescriptionAndSchemaAreTheSpecText(t *testing.T) {
	tool := difftool.New(openState(t))
	if tool.Name() != "diff" {
		t.Fatalf("name = %q, want diff", tool.Name())
	}
	const wantDescription = "diff a tool call's result against its previous observation, or the working tree against HEAD.\n\n" +
		"mode 'files': the tool shells out to `git diff` and says so: the working tree against HEAD (or ref, optional), optional paths; a non-git cwd refuses loud, naming the reason.\n\n" +
		"mode 'last': the recorded tool calls of this session only (a resumed session is the same session; another session is another world): the newest result of the same call (tool name + exact args; key order and whitespace do not matter, values do) against its n-th previous (n optional, default 1). a read path over state the harness already recorded; nothing new is written.\n\n" +
		"the reply is a unified diff (context 3, ANSI-free, capped, '… K more lines'), or the word 'identical', or 'no earlier observation'.\n\n" +
		"Guidelines: 'did my change actually apply' -> last, with the tool and args of the call that made the change; tree against HEAD -> files; diff of arbitrary strings -> python, not this."
	if tool.Description() != wantDescription {
		t.Fatalf("description = %q, want the spec's verbatim text", tool.Description())
	}
	const wantSchema = `{
  "type": "object",
  "required": ["mode"],
  "properties": {
    "mode": {
      "type": "string",
      "enum": ["files", "last"],
      "description": "files: the working tree against HEAD (git diff); last: the previous observation of the same tool call"
    },
    "ref": {
      "type": "string",
      "description": "files only: the ref to diff against (default HEAD)"
    },
    "paths": {
      "type": "array",
      "items": {"type": "string"},
      "description": "files only: restrict the diff to these paths"
    },
    "tool": {
      "type": "string",
      "description": "last only: the tool name of the observed call"
    },
    "args": {
      "type": "object",
      "description": "last only: the exact args of that call; matched by canonical equality (key order and whitespace do not matter, values do)"
    },
    "n": {
      "type": "integer",
      "minimum": 1,
      "description": "last only: the n-th previous observation (default 1)"
    }
  }
}`
	if string(tool.Schema()) != wantSchema {
		t.Fatalf("schema = %s, want the spec's verbatim JSON", tool.Schema())
	}
	// the schema is well-formed JSON (the wire's gate).
	var probe map[string]any
	if err := json.Unmarshal(tool.Schema(), &probe); err != nil {
		t.Fatalf("the schema must be valid JSON: %v", err)
	}
}

// --- the pinned refusals (loud, naming the reason) ---

func TestPinnedRefusals(t *testing.T) {
	w := newWorld(t)
	tool := difftool.New(w.db)
	sess := core.NewSession()
	sess.ID = w.sid
	ctx := core.WithSession(context.Background(), sess)
	cases := []struct {
		name string
		args string
		want string
	}{
		{"mode missing", `{}`, "diff: mode required (files|last)"},
		{"mode unknown", `{"mode":"both"}`, `diff: unknown mode "both" (files|last)`},
		{"last without tool and args", `{"mode":"last"}`, "diff last: tool and args required"},
		{"last without args", `{"mode":"last","tool":"bash"}`, "diff last: tool and args required"},
		{"n zero", `{"mode":"last","tool":"bash","args":{"command":"ls"},"n":0}`, "diff last: n must be >= 1"},
		{"n negative", `{"mode":"last","tool":"bash","args":{"command":"ls"},"n":-2}`, "diff last: n must be >= 1"},
		{"n not an integer", `{"mode":"last","tool":"bash","args":{"command":"ls"},"n":"2"}`, "diff last: n must be >= 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := tool.Exec(ctx, json.RawMessage(c.args))
			if err == nil {
				t.Fatalf("must refuse")
			}
			if err.Error() != c.want {
				t.Fatalf("voice = %q, want %q", err.Error(), c.want)
			}
		})
	}
}

// --- a frontend for the recorder (the re-landed-tail case) ---

type nullFrontend struct{}

func (nullFrontend) Input(context.Context) (string, error) { return "", io.EOF }
func (nullFrontend) Notify(core.Event)                     {}
