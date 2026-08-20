package scheduler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type JailProfile struct {
	Bwrap     string
	Binary    string
	Prompt    string
	BaseURL   string
	Model     string
	Cwd       string
	KernelDir string
	SockPath  string
	Binds     []string
}

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

func SandboxProfile(s string) (string, error) {
	if s == "" {
		return "jailed", nil
	}
	if s == "jailed" || s == "off" {
		return s, nil
	}
	return "", fmt.Errorf("sandbox: expected \"jailed\" or \"off\", got %q", s)
}

func PlatformRefusal(gos string) string {
	return "sandbox: the worker jail is linux-only (profile jailed; this build is " + gos + ")"
}

func BwrapRefusal() string {
	return "sandbox: bwrap not found on $PATH (profile jailed): install bubblewrap, or set sandbox to \"off\" (sandboxBinds names the extra paths a jailed worker needs)"
}

func KernelRefusal(kernelDir string) string {
	return "sandbox: the kernel directory is absent: " + kernelDir + " (the python host's source; the operator's rig home)"
}

func ScratchRefusal(scratch string, err error) string {
	return "sandbox: the scratch home: " + scratch + ": " + err.Error()
}

func SocketRefusal(sock string, err error) string {
	return "sandbox: the socket proxy: " + sock + ": " + err.Error()
}

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

	scratch := filepath.Join(cwd, ".rig-job")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return nil, nil, "", ScratchRefusal(scratch, err), nil
	}

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
