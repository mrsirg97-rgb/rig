package room

import "testing"

func TestTheIdentityStandsWithoutProc(t *testing.T) {
	first, err := identity()
	if err != nil {
		t.Fatalf("darwin has no /proc and the identity must still stand: %v", err)
	}
	second, err := identity()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("one process, one identity: %q then %q", first, second)
	}
	start, err := startTime()
	if err != nil || start <= 0 {
		t.Fatalf("the start time comes from the process row: %d, %v", start, err)
	}
}
