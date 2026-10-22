//go:build !linux

package scheduler_test

import (
	"runtime"
	"testing"

	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
)

func TestTheJailedAndLandlockProfilesRefuseOffLinux(t *testing.T) {
	for _, tc := range []struct {
		profile string
		want    string
	}{
		{"", sched.PlatformRefusal(runtime.GOOS)},
		{"jailed", sched.PlatformRefusal(runtime.GOOS)},
		{"landlock", sched.LandlockPlatformRefusal(runtime.GOOS)},
	} {
		h, key := setupJob(t, t.TempDir(), nil)
		spawn := &envSpawn{}
		before := h.ct.text
		mustOK(t, sched.RunJob(key, runSandboxOpts(h, spawn.spawn, tc.profile)))
		rec := runEvents(t, h, "")[0]
		if rec.Args["status"] != "skip" {
			t.Fatalf("profile %q off linux is a recorded skip, got %v", tc.profile, rec.Args)
		}
		if got := toString(rec.Args["reason"]); got != tc.want {
			t.Fatalf("profile %q: the refusal must name the platform, got %q want %q", tc.profile, got, tc.want)
		}
		if len(spawn.calls) != 0 {
			t.Fatalf("profile %q: no spawn off linux", tc.profile)
		}
		if h.ct.text != before {
			t.Fatalf("profile %q: the crontab must stay untouched", tc.profile)
		}
	}
}
