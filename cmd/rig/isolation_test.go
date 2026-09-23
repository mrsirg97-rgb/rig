package main

import (
	"os"
	"testing"

	"github.com/mrsirg97-rgb/rig/testenv"
)

func TestTestsNeverSeeTheOperatorHome(t *testing.T) {
	if os.Getenv("HOME") == testenv.OperatorHome {
		t.Fatalf("the suite must run in an isolated home, not %q", testenv.OperatorHome)
	}
}
