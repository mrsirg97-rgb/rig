package scheduler_test

import (
	"testing"

	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	"github.com/mrsirg97-rgb/rig/testenv"
)

func TestMain(m *testing.M) {
	sched.Transport = testenv.Transport()
	testenv.Main(m)
}
