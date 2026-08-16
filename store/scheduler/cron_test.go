package scheduler

import (
	"os"
	"regexp"
	"testing"
	"time"
)

// pane's scheduler-cron cases, by name. Deterministic dates: pin UTC
// before any time arithmetic (this runtime's equivalent of pane's
// process.env.TZ = "UTC"; Go reads it lazily on first local-time use).
func init() { os.Setenv("TZ", "UTC") }

// --- ValidateCron: acceptance ---

func TestValidateCronAcceptsStandardExpressions(t *testing.T) {
	for _, expr := range []string{
		"0 */4 * * *", // the watchdog line
		"*/15 9-17 * * 1-5",
		"5 4 * * 0",
		"0 0 1 1 *",
		"30 14 15 8 *",
		"1,2,3 0 * * *",
		"1-30/2 0 1 * *",
		"0 0 * * 7", // 7 == Sunday
		"  0   * * * *  ", // surrounding/multi whitespace
		"* * * * *",
	} {
		if _, err := ValidateCron(expr); err != nil {
			t.Errorf("ValidateCron(%q): %v", expr, err)
		}
	}
}

func TestValidateCronExpandsValuesAndSteps(t *testing.T) {
	p, err := ValidateCron("0 */4 * * *")
	if err != nil {
		t.Fatal(err)
	}
	assertInts(t, "minute", p.Minute.Values, []int{0})
	assertInts(t, "hour", p.Hour.Values, []int{0, 4, 8, 12, 16, 20})
	assertInts(t, "dom", p.Dom.Values, ints(1, 31))
	assertInts(t, "month", p.Month.Values, ints(1, 12))
	assertInts(t, "dow", p.Dow.Values, []int{0, 1, 2, 3, 4, 5, 6})

	q, err := ValidateCron("1-30/2 9-17 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
	assertInts(t, "minute", q.Minute.Values, []int{1, 3, 5, 7, 9, 11, 13, 15, 17, 19, 21, 23, 25, 27, 29})
	assertInts(t, "hour", q.Hour.Values, ints(9, 17))
	assertInts(t, "dow", q.Dow.Values, []int{1, 2, 3, 4, 5})
}

func TestValidateCronDow7NormalizesTo0(t *testing.T) {
	p, err := ValidateCron("0 0 * * 0,7")
	if err != nil {
		t.Fatal(err)
	}
	assertInts(t, "dow", p.Dow.Values, []int{0})
}

func TestValidateCronIsStarTracksTheStar(t *testing.T) {
	cases := []struct {
		expr string
		want bool
	}{
		{"* * * * *", true},
		{"* * */2 * *", true}, // */2 is unrestricted
		{"* * 5 * *", false},
		{"* * 1-5 * *", false},
	}
	for _, c := range cases {
		p, err := ValidateCron(c.expr)
		if err != nil {
			t.Fatal(err)
		}
		if p.Dom.IsStar != c.want {
			t.Errorf("%q: dom.IsStar = %v, want %v", c.expr, p.Dom.IsStar, c.want)
		}
	}
	d, err := ValidateCron("* * * * 7")
	if err != nil {
		t.Fatal(err)
	}
	if d.Dow.IsStar != false {
		t.Error("* * * * 7: dow.IsStar must be false")
	}
}

// --- ValidateCron: rejection, by boundary name ---

func TestValidateCronRejectsWrongFieldCounts(t *testing.T) {
	for _, expr := range []string{"* * * *", "* * * * * *", ""} {
		_, err := ValidateCron(expr)
		if err == nil {
			t.Errorf("ValidateCron(%q): accepted, want /5 fields/", expr)
			continue
		}
		matchRe(t, err, `5 fields`)
	}
}

func TestValidateCronRejectsAtMacros(t *testing.T) {
	_, err := ValidateCron("@daily")
	matchRe(t, err, `macros`)
}

func TestValidateCronRejectsGarbageAndNames(t *testing.T) {
	for _, expr := range []string{"a * * * *", "* * * jan *", "* * * * mon"} {
		_, err := ValidateCron(expr)
		matchRe(t, err, `invalid`)
	}
}

func TestValidateCronRejectsOutOfRangeValues(t *testing.T) {
	cases := []struct{ expr, re string }{
		{"60 * * * *", `minute.*0-59`},
		{"* 24 * * *", `hour.*0-23`},
		{"* * 0 * *", `day-of-month.*1-31`},
		{"* * 32 * *", `day-of-month.*1-31`},
		{"* * * 13 *", `month.*1-12`},
		{"* * * * 8", `day-of-week.*0-7`},
	}
	for _, c := range cases {
		_, err := ValidateCron(c.expr)
		matchRe(t, err, c.re)
	}
}

func TestValidateCronRejectsWrapRangesZeroStepsAndBareNumberSteps(t *testing.T) {
	cases := []struct{ expr, re string }{
		{"5-1 * * * *", `wrap`},
		{"*/0 * * * *", `step`},
		{"1/5 * * * *", `step`}, // vixie: step after * or range only
	}
	for _, c := range cases {
		_, err := ValidateCron(c.expr)
		matchRe(t, err, c.re)
	}
}

func TestValidateCronRejectsMalformedListsAndRanges(t *testing.T) {
	cases := []struct{ expr, re string }{
		{"1,,2 * * * *", `empty list item`},
		{"*-* * * * *", `invalid item`},
		{"1- * * * *", `invalid item`},
		{"-5 * * * *", `invalid item`},
	}
	for _, c := range cases {
		_, err := ValidateCron(c.expr)
		matchRe(t, err, c.re)
	}
}

// --- NextFire: exactness ---

func TestNextFireOfTheWatchdogLineFromJustAfterAFire(t *testing.T) {
	p, err := ValidateCron("0 */4 * * *")
	if err != nil {
		t.Fatal(err)
	}
	assertFire(t, p, ut(2026, 8, 15, 0, 2, 30), ut(2026, 8, 15, 4, 0, 0))
	assertFire(t, p, ut(2026, 8, 15, 3, 59, 0), ut(2026, 8, 15, 4, 0, 0))
}

func TestNextFireFiresStrictlyAfterTheReferenceTime(t *testing.T) {
	p, err := ValidateCron("0 */4 * * *")
	if err != nil {
		t.Fatal(err)
	}
	assertFire(t, p, ut(2026, 8, 15, 4, 0, 0), ut(2026, 8, 15, 8, 0, 0))
}

func TestNextFireOnceStyleMonthlyFireAndNextYearAfterAnExactHit(t *testing.T) {
	p, err := ValidateCron("30 14 15 8 *")
	if err != nil {
		t.Fatal(err)
	}
	assertFire(t, p, ut(2026, 8, 10, 0, 0), ut(2026, 8, 15, 14, 30))
	assertFire(t, p, ut(2026, 8, 15, 14, 30, 0), ut(2027, 8, 15, 14, 30))
}

func TestNextFireLeapDayWaitsForTheLeapYear(t *testing.T) {
	p, err := ValidateCron("0 0 29 2 *")
	if err != nil {
		t.Fatal(err)
	}
	assertFire(t, p, ut(2026, 1, 1, 0, 0), ut(2028, 2, 29, 0, 0))
	assertFire(t, p, ut(2028, 2, 29, 0, 0, 0), ut(2032, 2, 29, 0, 0))
}

func TestNextFireImpossibleDayHasNoNextFire(t *testing.T) {
	p, err := ValidateCron("0 0 30 2 *")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := NextFire(p, ut(2026, 1, 1, 0, 0)); ok {
		t.Fatal("impossible day must have no next fire")
	}
}

func TestNextFireDomDowUnionRestrictedDomOrRestrictedDow(t *testing.T) {
	// 12:00 on the 13th OR on a Friday. 2026-08-15 is a Saturday.
	p, err := ValidateCron("0 12 13 * 5")
	if err != nil {
		t.Fatal(err)
	}
	assertFire(t, p, ut(2026, 8, 15, 0, 0), ut(2026, 8, 21, 12, 0)) // Fri the 21st
	assertFire(t, p, ut(2026, 8, 13, 12, 0, 0), ut(2026, 8, 14, 12, 0))
	assertFire(t, p, ut(2026, 9, 14, 0, 0), ut(2026, 9, 18, 12, 0)) // Fri the 18th
}

func TestNextFireDomDowUnionAStarFieldDoesNotRestrict(t *testing.T) {
	// dom restricted, dow free -> only dom governs.
	p, err := ValidateCron("0 12 13 * *")
	if err != nil {
		t.Fatal(err)
	}
	assertFire(t, p, ut(2026, 8, 15, 0, 0), ut(2026, 9, 13, 12, 0))
	// dow restricted, dom free -> only dow governs (Friday).
	q, err := ValidateCron("0 12 * * 5")
	if err != nil {
		t.Fatal(err)
	}
	assertFire(t, q, ut(2026, 8, 15, 0, 0), ut(2026, 8, 21, 12, 0))
}

func TestNextFireEveryMinuteCronFiresTheNextMinuteBoundary(t *testing.T) {
	p, err := ValidateCron("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	assertFire(t, p, ut(2026, 8, 15, 10, 20, 45), ut(2026, 8, 15, 10, 21, 0))
	assertFire(t, p, ut(2026, 8, 15, 10, 20, 0), ut(2026, 8, 15, 10, 21, 0))
}

func TestNextFireHourRolloverCarriesIntoTheNextDay(t *testing.T) {
	p, err := ValidateCron("0 0 * * *")
	if err != nil {
		t.Fatal(err)
	}
	assertFire(t, p, ut(2026, 8, 15, 0, 0, 0), ut(2026, 8, 16, 0, 0)) // strictly after
	assertFire(t, p, ut(2026, 8, 15, 23, 59, 0), ut(2026, 8, 16, 0, 0))
}

// --- test plumbing ---

func ints(lo, hi int) []int {
	out := make([]int, 0, hi-lo+1)
	for v := lo; v <= hi; v++ {
		out = append(out, v)
	}
	return out
}

// ut: a UTC instant, carried as local (pinned UTC by init) — pane's Date
// constructor shape with defaulted seconds, deterministic under the pin.
func ut(y int, mo time.Month, d, h, mi int, s ...int) time.Time {
	sec := 0
	if len(s) > 0 {
		sec = s[0]
	}
	return time.Date(y, mo, d, h, mi, sec, 0, time.Local)
}

func assertFire(t *testing.T, p ParsedCron, from, want time.Time) {
	t.Helper()
	got, ok := NextFire(p, from)
	if !ok {
		t.Fatalf("NextFire(%v): no fire, want %v", from, want)
	}
	if !got.Equal(want) {
		t.Fatalf("NextFire(from=%v) = %v, want %v", from, got, want)
	}
}

func matchRe(t *testing.T, err error, re string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error matching /%s/, got nil", re)
	}
	if !regexp.MustCompile(re).MatchString(err.Error()) {
		t.Fatalf("error %q does not match /%s/", err.Error(), re)
	}
}

func assertInts(t *testing.T, field string, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s values = %v, want %v", field, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s values = %v, want %v", field, got, want)
		}
	}
}
