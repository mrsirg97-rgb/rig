package webtools

import (
	"os"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/testenv"
)

const operatorSecret = "4b1105cde10dbecbdbc5c02b61c996cb64d439d20f6f7cd11acba9eb5428a3ef"

func TestMain(m *testing.M) {
	testenv.Main(m)
}

func TestTestsNeverSeeTheOperatorHome(t *testing.T) {
	if os.Getenv("HOME") == testenv.OperatorHome {
		t.Fatalf("the suite must run in an isolated home, not %q", os.Getenv("HOME"))
	}
}
