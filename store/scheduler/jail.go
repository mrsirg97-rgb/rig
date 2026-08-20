package scheduler

// The worker jail (SPEC_SANDBOX 1, 5): the runner's profile
// composition, the fail-closed voices, and the sandbox profile's
// vocabulary. Pure: the argv is the spec's block, verbatim, from the
// resolved inputs; the refusals name their cause.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// JailProfile is the jail's resolved inputs (SPEC_SANDBOX 1): the
// runner fills it from the job's row, the operator's settings, and
// the rig home; JailArgv is its one output (the spec's block,
// verbatim).
type JailProfile struct {
	Bwrap     string   // the bwrap binary (resolved on $PATH)
	Binary    string   // the rig binary (the ro-bind and the exec payload)
	Prompt    string   // the job's prompt (the report-back appended)
	BaseURL   string   // the worker's endpoint (the bound socket, unix:<path>)
	Model     string   // the job's model
	Cwd       string   // the job's cwd (the rw bind)
	KernelDir string   // the operator home's kernel directory (the ro bind); empty = no kernel line
	SockPath  string   // the model socket (the one --bind, SPEC_SANDBOX 1)
	Binds     []string // the operator's extra binds (SPEC_SANDBOX 5; ro, unless an entry ends ":rw")
}

// JailArgv is the spec's profile, exactly (SPEC_SANDBOX 1): the
// unshare-all, the ro system, the proc/dev/tmpfs, the job's cwd rw,
// the kernel and the binary ro, the socket's one bind, the operator's
// extra binds, the chdir, and the worker payload. A bind entry that
// is not an absolute path (or carries a malformed marker) refuses,
// naming the entry.
func JailArgv(p JailProfile) ([]string, error) {
	argv := []string{
		p.Bwrap,
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
		"--bind", p.Cwd, p.Cwd,
	}
	if p.KernelDir != "" {
		argv = append(argv, "--ro-bind", p.KernelDir, p.KernelDir)
	}
	argv = append(argv, "--ro-bind", p.Binary, p.Binary)
	argv = append(argv, "--bind", p.SockPath, p.SockPath)
	for i, entry := range p.Binds {
		src, rw, err := parseBind(entry, i)
		if err != nil {
			return nil, err
		}
		if rw {
			argv = append(argv, "--bind", src, src)
		} else {
			argv = append(argv, "--ro-bind", src, src)
		}
	}
	argv = append(argv,
		"--chdir", p.Cwd,
		"--",
		p.Binary, "-p", p.Prompt,
		"-base-url", p.BaseURL,
		"-model", p.Model,
	)
	return argv, nil
}

// parseBind is one sandboxBinds entry (SPEC_SANDBOX 5): the path, ro
// by default, rw when the entry ends ":rw" (the suffix stripped from
// the path). A relative path is the operator's typo — named.
func parseBind(entry string, i int) (string, bool, error) {
	src := entry
	rw := false
	if strings.HasSuffix(entry, ":rw") {
		src = strings.TrimSuffix(entry, ":rw")
		rw = true
	}
	if !filepath.IsAbs(src) {
		return "", false, fmt.Errorf("sandboxBinds[%d]: expected an absolute path, got %q", i, entry)
	}
	return src, rw, nil
}

// SandboxProfile is the profile vocabulary (SPEC_SANDBOX 5): "jailed"
// (the default — fail closed) or "off" (the operator's explicit act).
// Empty descends to the default; anything else refuses, naming the
// known values.
func SandboxProfile(s string) (string, error) {
	if s == "" {
		return "jailed", nil
	}
	if s == "jailed" || s == "off" {
		return s, nil
	}
	return "", fmt.Errorf("sandbox: expected \"jailed\" or \"off\", got %q", s)
}

// PlatformRefusal is the linux-only voice (SPEC_SANDBOX 1): the named
// refusal on a non-linux build, the platform and the profile named.
func PlatformRefusal(gos string) string {
	return "sandbox: the worker jail is linux-only (profile jailed; this build is " + gos + ")"
}

// BwrapRefusal is the fail-closed voice (SPEC_SANDBOX 1, 5): bwrap
// absent on $PATH, profile jailed — the refusal names bwrap and the
// profile, and teaches both settings keys.
func BwrapRefusal() string {
	return "sandbox: bwrap not found on $PATH (profile jailed): install bubblewrap, or set sandbox to \"off\" (sandboxBinds names the extra paths a jailed worker needs)"
}

// KernelRefusal is the kernel-line voice (SPEC_SANDBOX 1): the
// operator home's kernel directory absent where the profile expects
// it — the python host's source is a fact of the home, and its
// absence is the refusal.
func KernelRefusal(kernelDir string) string {
	return "sandbox: the kernel directory is absent: " + kernelDir + " (the python host's source; the operator's rig home)"
}

// ScratchRefusal names the scratch home's failure (SPEC_SANDBOX 1):
// the worker's home inside the job's cwd cannot be created — the run
// refuses before any spawn.
func ScratchRefusal(scratch string, err error) string {
	return "sandbox: the scratch home: " + scratch + ": " + err.Error()
}

// SocketRefusal names the socket proxy's failure (SPEC_SANDBOX 1):
// the one hole could not be punched — the run refuses before any
// spawn.
func SocketRefusal(sock string, err error) string {
	return "sandbox: the socket proxy: " + sock + ": " + err.Error()
}

// jailSpawn is the jailed worker's preflight and spawn ingredients
// (SPEC_SANDBOX 1): the fail-closed checks in order — the platform,
// bwrap on $PATH, the kernel line's source, the scratch home, the
// socket hole — then the spec's profile argv. A non-empty refuse is
// the recorded skip's reason (nothing spawns); a non-nil err is the
// runner's loud failure. On success the caller closes the proxy after
// the spawn and carries the scratch home as the worker's RIG_HOME.
func jailSpawn(opts RunOpts, cwd string, workerCmd []string, model, prompt string) ([]string, *SocketProxy, string, string, error) {
	if runtime.GOOS != "linux" {
		return nil, nil, "", PlatformRefusal(runtime.GOOS), nil
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, nil, "", BwrapRefusal(), nil
	}
	kernelDir := ""
	if opts.RigHome != "" {
		kernelDir = filepath.Join(opts.RigHome, "kernel")
		if _, err := os.Stat(kernelDir); err != nil {
			return nil, nil, "", KernelRefusal(kernelDir), nil
		}
	}
	// the scratch home (SPEC_SANDBOX 1): the worker's rig home inside
	// the job's cwd — a worker that cannot write the operator's stores
	// cannot poison the next session's transcript.
	scratch := filepath.Join(cwd, ".rig-job")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return nil, nil, "", ScratchRefusal(scratch, err), nil
	}
	// the one hole (SPEC_SANDBOX 1): the socket proxy, bound into the
	// jail by the profile's --bind line (the socket rides the cwd's
	// rw bind too). The worker's base URL points at it.
	sock := filepath.Join(cwd, ".rig-job.sock")
	proxy, err := NewSocketProxy(sock, opts.SwapURL)
	if err != nil {
		return nil, nil, "", SocketRefusal(sock, err), nil
	}
	argv, err := JailArgv(JailProfile{
		Bwrap:     bwrap,
		Binary:    workerCmd[0],
		Prompt:    prompt,
		BaseURL:   "unix:" + sock,
		Model:     model,
		Cwd:       cwd,
		KernelDir: kernelDir,
		SockPath:  sock,
		Binds:     opts.SandboxBinds,
	})
	if err != nil {
		proxy.Close()
		return nil, nil, "", "", err
	}
	return argv, proxy, scratch, "", nil
}
