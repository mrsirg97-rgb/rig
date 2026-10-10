package scheduler_test

import (
	"os"
	"strings"
	"testing"
)

func TestTheSlotReadIsRetired(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		name := f.Name()
		if f.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, gone := range []string{"func FreeSlots", "func FleetCapacity", "func slotRead", "type slotSet"} {
			if strings.Contains(string(src), gone) {
				t.Fatalf("the retired slot read survives in %s: %s", name, gone)
			}
		}
	}
}
