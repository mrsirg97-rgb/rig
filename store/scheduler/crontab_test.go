package scheduler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const RUNNER = "/x/rig run-job"

const testHome = "/home/u/.rig"

const otherHome = "/home/u/.orbit"

var foreign = strings.Join([]string{
	"# foreign comment at top",
	"SHELL=/bin/bash",
	`MAILTO=""`,
	"0 5 * * * /usr/local/bin/backup.sh   # user's own trailing comment",
	"15 3 * * 0  /opt/tool --weekly",
	"",
	"# a note mentioning rig-scheduler in prose",
	"30 6 * * * echo hi # rig-scheduler:NOTOURS  # tag not trailing",
}, "\n")

func TestLineForBuildsTheExactTaggedFormat(t *testing.T) {
	want := `0 */4 * * * /x/rig run-job j1  # rig-scheduler:` + TagHome(testHome) + `:j1`
	if got := LineFor("j1", "0 */4 * * *", RUNNER, testHome); got != want {
		t.Fatalf("LineFor(j1) = %q, want %q", got, want)
	}
	if !strings.HasPrefix(want, "0 */4 * * * /x/rig run-job j1  # rig-scheduler:") {
		t.Fatalf("the writer must emit only the rig-scheduler tag: %q", want)
	}
}

func TestTagHomeIsTheShortSha1OfTheHomePath(t *testing.T) {
	if TagHome(testHome) == TagHome(otherHome) {
		t.Fatal("two homes must own disjoint tag namespaces")
	}
	if got := TagHome(testHome); len(got) != 12 || !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(got) {
		t.Fatalf("TagHome = %q, want 12 hex chars", got)
	}
}

func TestUpsertAppendsATaggedLineForeignLinesSurviveByteIdentical(t *testing.T) {
	text, added := UpsertLine(foreign, "j1", "0 */4 * * *", RUNNER, testHome)
	if !added {
		t.Fatal("new key must report added=true")
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	want := strings.Split(foreign, "\n")
	if len(lines) < len(want) {
		t.Fatalf("result lost foreign lines")
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("foreign line %d drifted: %q", i, lines[i])
		}
	}
	tagged := `0 */4 * * * /x/rig run-job j1  # rig-scheduler:` + TagHome(testHome) + `:j1`
	if last := lines[len(lines)-1]; last != tagged {
		t.Fatalf("last line %q", last)
	}
	if !strings.HasSuffix(text, tagged+"\n") {
		t.Fatal("must end with exactly one trailing newline")
	}
}

func TestUpsertOnAnEmptyCrontabYieldsExactlyOneLine(t *testing.T) {
	text, added := UpsertLine("", "j1", "0 0 * * *", RUNNER, testHome)
	want := `0 0 * * * /x/rig run-job j1  # rig-scheduler:` + TagHome(testHome) + `:j1` + "\n"
	if !added || text != want {
		t.Fatalf("text %q added %v", text, added)
	}
}

func TestUpsertReplacesAnExistingKeyInPlace(t *testing.T) {
	seeded := foreign + "\n0 0 * * * /x/rig run-job j9  # rig-scheduler:" + TagHome(testHome) + ":j9\n"
	text, added := UpsertLine(seeded, "j9", "30 1 * * *", RUNNER, testHome)
	if added {
		t.Fatal("existing key must replace, not append")
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	var found []string
	for _, l := range lines {
		if strings.Contains(l, "rig-scheduler:"+TagHome(testHome)+":j9") {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("j9 lines = %d, want 1", len(found))
	}
	if found[0] != `30 1 * * * /x/rig run-job j9  # rig-scheduler:`+TagHome(testHome)+`:j9` {
		t.Fatalf("replaced line %q", found[0])
	}
	idx := -1
	for i, l := range lines {
		if l == found[0] {
			idx = i
		}
	}
	if want := len(strings.Split(foreign, "\n")); idx != want {
		t.Fatalf("position = %d, want %d (original position kept)", idx, want)
	}
}

func TestUpsertLeavesAnotherHomesSameKeyLineAlone(t *testing.T) {
	other := `0 6 * * * /x/rig run-job j1  # rig-scheduler:` + TagHome(otherHome) + `:j1`
	seeded := foreign + "\n" + other + "\n0 0 * * * /x/rig run-job j1  # rig-scheduler:" + TagHome(testHome) + ":j1\n"
	text, added := UpsertLine(seeded, "j1", "30 1 * * *", RUNNER, testHome)
	if added {
		t.Fatal("this home's existing key must replace, not append")
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if !strings.Contains(strings.Join(lines, "\n"), other) {
		t.Fatal("another home's line must survive byte-identical")
	}
	ours := 0
	for _, l := range lines {
		if strings.Contains(l, "rig-scheduler:"+TagHome(testHome)+":j1") {
			ours++
		}
	}
	if ours != 1 || lines[len(lines)-1] != `30 1 * * * /x/rig run-job j1  # rig-scheduler:`+TagHome(testHome)+`:j1` || lines[len(lines)-2] != other {
		t.Fatalf("this home's line must be replaced in place, ours=%d:\n%s", ours, text)
	}
}

func TestSetPausedCommentsTheLineTagStaysDiscoverable(t *testing.T) {
	seeded := foreign + "\n0 0 * * * /x/rig run-job j1  # rig-scheduler:" + TagHome(testHome) + ":j1\n"
	text, found := SetPaused(seeded, "j1", true, testHome)
	if !found {
		t.Fatal("must find the line")
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	want := strings.Split(foreign, "\n")
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("foreign line %d drifted", i)
		}
	}
	if last := lines[len(lines)-1]; last != `# 0 0 * * * /x/rig run-job j1  # rig-scheduler:`+TagHome(testHome)+`:j1` {
		t.Fatalf("last line %q", last)
	}
	scanned := Scan(text, testHome)
	var j1 *TaggedLine
	for i := range scanned {
		if scanned[i].Key == "j1" {
			j1 = &scanned[i]
		}
	}
	if j1 == nil || !j1.Paused || j1.Cron != "0 0 * * *" {
		t.Fatalf("scan of paused line = %+v", j1)
	}
}

func TestSetPausedIsIdempotent(t *testing.T) {
	paused := foreign + "\n# 0 0 * * * /x/rig run-job j1  # rig-scheduler:" + TagHome(testHome) + ":j1\n"
	text, found := SetPaused(paused, "j1", true, testHome)
	if !found || text != paused {
		t.Fatalf("idempotent pause drifted: %q", text)
	}
}

func TestSetPausedOfAMissingKeyChangesNothing(t *testing.T) {
	text, found := SetPaused(foreign, "j404", true, testHome)
	if found {
		t.Fatal("missing key must report found=false")
	}
	if text != foreign+"\n" {
		t.Fatalf("text drifted (normalize adds the trailing newline only): %q", text)
	}
}

func TestResumeStripsExactlyThePrefixByteIdenticalToTheActiveLine(t *testing.T) {
	active := `0 0 * * * /x/rig run-job j1  # rig-scheduler:` + TagHome(testHome) + `:j1`
	paused := foreign + "\n# " + active + "\n"
	text, found := SetPaused(paused, "j1", false, testHome)
	if !found {
		t.Fatal("must find the line")
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if last := lines[len(lines)-1]; last != active {
		t.Fatalf("resumed line %q", last)
	}
}

func TestRemoveDeletesTheLineAndLeavesNoTrace(t *testing.T) {
	active := foreign + "\n0 0 * * * /x/rig run-job j1  # rig-scheduler:" + TagHome(testHome) + ":j1\n"
	text, found := RemoveLine(active, "j1", testHome)
	if !found || strings.Contains(text, "rig-scheduler:"+TagHome(testHome)+":j1") {
		t.Fatalf("active remove: found %v text %q", found, text)
	}
	if text != foreign+"\n" {
		t.Fatalf("active remove text %q", text)
	}
	pausedLine := foreign + "\n# 0 0 * * * /x/rig run-job j1  # rig-scheduler:" + TagHome(testHome) + ":j1\n"
	text, found = RemoveLine(pausedLine, "j1", testHome)
	if !found || strings.Contains(text, "rig-scheduler:"+TagHome(testHome)+":j1") {
		t.Fatalf("paused remove: found %v text %q", found, text)
	}
}

func TestRemoveOfAMissingKeyReportsNotFound(t *testing.T) {
	if _, found := RemoveLine(foreign, "j404", testHome); found {
		t.Fatal("missing key must report found=false")
	}
}

func TestScanFindsTaggedLinesAndExtractsCronFields(t *testing.T) {
	text := foreign +
		"\n0 */4 * * * /x/rig run-job j1  # pane-scheduler:j1" +
		"\n# 5 4 * * * /x/rig run-job cwd-abc123def456:j2  # pane-scheduler:cwd-abc123def456:j2" +
		"\n7 3 16 8 * /x/rig run-job j3  # rig-scheduler:" + TagHome(testHome) + ":j3"
	found := Scan(text, testHome)
	var j1, j2, j3 *TaggedLine
	for i := range found {
		switch found[i].Key {
		case "j1":
			j1 = &found[i]
		case "cwd-abc123def456:j2":
			j2 = &found[i]
		case "j3":
			j3 = &found[i]
		}
	}
	if j1 == nil || j1.Paused || j1.Cron != "0 */4 * * *" {
		t.Fatalf("the reader must accept the old tag: j1 = %+v", j1)
	}
	if j2 == nil || !j2.Paused || j2.Cron != "5 4 * * *" {
		t.Fatalf("the old-tag legacy key must still read: j2 = %+v", j2)
	}
	if j3 == nil || j3.Paused || j3.Cron != "7 3 16 8 *" {
		t.Fatalf("j3 = %+v", j3)
	}
}

func TestScanIgnoresAnotherHomesTaggedLine(t *testing.T) {
	text := strings.Join([]string{
		"0 6 * * * /x/rig run-job j1  # rig-scheduler:" + TagHome(otherHome) + ":j1",
		"0 7 * * * /x/rig run-job j1  # rig-scheduler:" + TagHome(testHome) + ":j1",
	}, "\n")
	found := Scan(text, testHome)
	if len(found) != 1 || found[0].Key != "j1" {
		t.Fatalf("scan = %+v, want only this home's line", found)
	}
}

func TestScanIgnoresLookalikes(t *testing.T) {
	text := strings.Join([]string{
		"# prose about rig-scheduler and friends",
		"30 6 * * * echo hi # rig-scheduler:NOTOURS  # tag not trailing",
		"# rig-scheduler:standalone",
		`0 0 * * * /x/rig run-job j1  # pane-scheduler:j1`,
	}, "\n")
	found := Scan(text, testHome)
	if len(found) != 1 || found[0].Key != "j1" {
		t.Fatalf("scan = %+v, want exactly j1", found)
	}
}

func TestNormalizeExactlyOneTrailingNewline(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"a\n", "a\n"},
		{"a", "a\n"},
		{"a\n\n\n", "a\n"},
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.want {
			t.Fatalf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func writeFakeCrontab(t *testing.T, dir, body string) string {
	t.Helper()
	bin := filepath.Join(dir, "crontab")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestShimAbsentSpoolReadsAsEmptyRoundTripWorks(t *testing.T) {
	dir := t.TempDir()
	spool := filepath.Join(dir, "spool")
	bin := writeFakeCrontab(t, dir, `if [ "$1" = "-l" ]; then
  if [ -f "$SPOOL" ]; then cat "$SPOOL"; else echo "crontab: no crontab for test" >&2; exit 1; fi
else
  mkdir -p "$(dirname "$SPOOL")"; cat > "$SPOOL"
fi`)
	t.Setenv("SPOOL", spool)
	shim := RealCrontab(bin)
	got, err := shim.List()
	if err != nil {
		t.Fatalf("absent spool must read as empty: %v", err)
	}
	if got != "" {
		t.Fatalf("absent spool read %q", got)
	}
	if err := shim.Install("0 0 * * * /bin/true\n"); err != nil {
		t.Fatalf("install: %v", err)
	}
	got, err = shim.List()
	if err != nil || got != "0 0 * * * /bin/true\n" {
		t.Fatalf("round-trip = %q, %v", got, err)
	}
}

func TestShimUnexpectedStderrRefusesLoudly(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeCrontab(t, dir, `echo "PAM: user not authorized" >&2
exit 1`)
	shim := RealCrontab(bin)
	_, err := shim.List()
	if err == nil || !regexp.MustCompile(`crontab list failed.*PAM: user not authorized`).MatchString(err.Error()) {
		t.Fatalf("error %v", err)
	}
}

func TestShimMissingBinaryRefusesLoudly(t *testing.T) {
	shim := RealCrontab("/nonexistent/no-such-crontab")
	if _, err := shim.List(); err == nil || !regexp.MustCompile(`binary not found`).MatchString(err.Error()) {
		t.Fatalf("list error %v", err)
	}
	if err := shim.Install("x"); err == nil || !regexp.MustCompile(`binary not found`).MatchString(err.Error()) {
		t.Fatalf("install error %v", err)
	}
}
