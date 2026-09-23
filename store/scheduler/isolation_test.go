package scheduler_test

import (
	"os"
	"strings"
	"testing"

	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	"github.com/mrsirg97-rgb/rig/testenv"
)

func TestTestsNeverSeeTheOperatorHome(t *testing.T) {
	if os.Getenv("HOME") == testenv.OperatorHome {
		t.Fatalf("the suite must run in an isolated home, not %q", testenv.OperatorHome)
	}
}

func TestRealFetchRidesTheTestTransport(t *testing.T) {
	fetch := sched.RealFetch(0)
	_, err := fetch("http://127.0.0.1:8090/running")
	if err == nil || !strings.Contains(err.Error(), "testenv") {
		t.Fatalf("the real fetch must refuse a host that is not an httptest server, got %v", err)
	}
}
