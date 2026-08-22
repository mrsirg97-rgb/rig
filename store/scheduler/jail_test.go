package scheduler_test

import (
	"strings"
	"testing"

	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

func TestJailArgvIsTheSpecProfileVerbatim(t *testing.T) {
	p := sched.JailProfile{
		Bwrap:     "/usr/bin/bwrap",
		Binary:    "/opt/rig/bin/rig",
		Prompt:    "do the thing\n\nReport back: ...",
		BaseURL:   "unix:/ws/j/.rig-job.sock",
		Model:     "qwen3.8-workers",
		Cwd:       "/ws/j",
		KernelDir: "/home/op/.rig/kernel",
		SockPath:  "/ws/j/.rig-job.sock",
	}
	argv, err := sched.JailArgv(p)
	if err != nil {
		t.Fatalf("JailArgv: %v", err)
	}
	want := []string{
		"/usr/bin/bwrap",
		"--unshare-all", "--die-with-parent",
		"--ro-bind", "/usr", "/usr",
		"--ro-bind", "/lib", "/lib",
		"--ro-bind", "/lib64", "/lib64",
		"--ro-bind", "/bin", "/bin",
		"--ro-bind", "/sbin", "/sbin",
		"--ro-bind", "/etc", "/etc",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--bind", "/ws/j", "/ws/j",
		"--ro-bind", "/home/op/.rig/kernel", "/home/op/.rig/kernel",
		"--ro-bind", "/opt/rig/bin/rig", "/opt/rig/bin/rig",
		"--bind", "/ws/j/.rig-job.sock", "/ws/j/.rig-job.sock",
		"--chdir", "/ws/j",
		"--",
		"/opt/rig/bin/rig", "-p", "do the thing\n\nReport back: ...",
		"-base-url", "unix:/ws/j/.rig-job.sock",
		"-model", "qwen3.8-workers",
	}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v (len %d), want the spec's profile (len %d)", argv, len(argv), len(want))
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q\nfull: %v", i, argv[i], want[i], argv)
		}
	}
}

func TestJailArgvNoKernelLineWhenTheKernelDirIsAbsent(t *testing.T) {
	p := sched.JailProfile{
		Bwrap:    "/usr/bin/bwrap",
		Binary:   "/opt/rig/bin/rig",
		Prompt:   "p",
		BaseURL:  "unix:/ws/j/.rig-job.sock",
		Model:    "m",
		Cwd:      "/ws/j",
		SockPath: "/ws/j/.rig-job.sock",
	}
	argv, err := sched.JailArgv(p)
	if err != nil {
		t.Fatalf("JailArgv: %v", err)
	}
	for i, a := range argv {
		if a == "--ro-bind" && i+2 < len(argv) && strings.Contains(argv[i+1], "kernel") {
			t.Fatalf("an absent kernel dir must not produce a kernel bind: %v", argv)
		}
	}
}

func TestJailArgvBindsFollowTheRwSuffix(t *testing.T) {
	p := sched.JailProfile{
		Bwrap:     "/usr/bin/bwrap",
		Binary:    "/opt/rig/bin/rig",
		Prompt:    "p",
		BaseURL:   "unix:/ws/j/.rig-job.sock",
		Model:     "m",
		Cwd:       "/ws/j",
		KernelDir: "/home/op/.rig/kernel",
		SockPath:  "/ws/j/.rig-job.sock",
		Binds:     []string{"/dev/nvidia0", "/dev/nvidiactl", "/data:rw"},
	}
	argv, err := sched.JailArgv(p)
	if err != nil {
		t.Fatalf("JailArgv: %v", err)
	}
	join := strings.Join(argv, " ")
	if !strings.Contains(join, "--ro-bind /dev/nvidia0 /dev/nvidia0") {
		t.Fatalf("the bare entry must be a ro-bind: %v", argv)
	}
	if !strings.Contains(join, "--ro-bind /dev/nvidiactl /dev/nvidiactl") {
		t.Fatalf("the bare entry must be a ro-bind: %v", argv)
	}
	if !strings.Contains(join, "--bind /data /data") {
		t.Fatalf("the :rw entry must be an rw bind of the bare path: %v", argv)
	}
	if strings.Contains(join, ":rw") {
		t.Fatalf("the :rw suffix must be stripped from the path: %v", argv)
	}
}

func TestJailArgvRefusesRelativeBinds(t *testing.T) {
	p := sched.JailProfile{
		Bwrap:    "/usr/bin/bwrap",
		Binary:   "/opt/rig/bin/rig",
		Prompt:   "p",
		BaseURL:  "unix:/ws/j/.rig-job.sock",
		Model:    "m",
		Cwd:      "/ws/j",
		SockPath: "/ws/j/.rig-job.sock",
		Binds:    []string{"relative/path"},
	}
	if _, err := sched.JailArgv(p); err == nil ||
		!strings.Contains(err.Error(), "sandboxBinds[0]") ||
		!strings.Contains(err.Error(), "relative/path") {
		t.Fatalf("a relative bind must refuse naming the entry, got %v", err)
	}
}

func TestSandboxProfileNormalizesAndRefuses(t *testing.T) {
	if got, err := sched.SandboxProfile(""); err != nil || got != "jailed" {
		t.Fatalf("SandboxProfile(\"\") = (%q, %v), want (jailed, nil)", got, err)
	}
	for _, v := range []string{"jailed", "off"} {
		if got, err := sched.SandboxProfile(v); err != nil || got != v {
			t.Fatalf("SandboxProfile(%q) = (%q, %v), want (%q, nil)", v, got, err, v)
		}
	}
	if _, err := sched.SandboxProfile("maybe"); err == nil ||
		!strings.Contains(err.Error(), `"jailed" or "off"`) ||
		!strings.Contains(err.Error(), "maybe") {
		t.Fatalf("an unknown profile must refuse naming the vocabulary, got %v", err)
	}
}

func TestJailRefusalVoices(t *testing.T) {
	v := sched.PlatformRefusal("darwin")
	if !strings.Contains(v, "darwin") || !strings.Contains(v, "linux") || !strings.Contains(v, "jailed") {
		t.Fatalf("the platform refusal must name the platform, linux, and the profile: %q", v)
	}
	v = sched.BwrapRefusal()
	for _, needle := range []string{"bwrap", "jailed", `sandbox`, `sandboxBinds`} {
		if !strings.Contains(v, needle) {
			t.Fatalf("the bwrap refusal must name %q: %q", needle, v)
		}
	}
}
