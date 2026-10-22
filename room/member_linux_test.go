package room

import (
	"path/filepath"
	"testing"
)

func TestAStatReadFailureFailsTheIdentity(t *testing.T) {
	old := procStat
	procStat = filepath.Join(t.TempDir(), "absent")
	t.Cleanup(func() { procStat = old })
	if _, err := startTime(); err == nil {
		t.Fatal("an unreadable stat must fail the identity")
	}
}
