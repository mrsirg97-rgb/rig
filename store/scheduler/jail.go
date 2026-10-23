package scheduler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type JailProfile struct {
	Bwrap     string
	Binary    string
	BaseURL   string
	Model     string
	Effort    string
	SessionID string
	Cwd       string
	KernelDir string
	SockPath  string
	Binds     []string
	Python    string
	FFmpeg    string
	Browser   string
	StateDir  string
	Allow     string
	Env       []string
}

var jailRO = []string{"/usr", "/lib", "/lib64", "/bin", "/sbin", "/etc"}

func JailArgv(p JailProfile) ([]string, error) {
	argv := []string{
		p.Bwrap,
		"--unshare-all", "--die-with-parent", "--clearenv",
	}
	for _, e := range p.Env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			return nil, fmt.Errorf("jail env %q: expected KEY=VALUE", e)
		}
		argv = append(argv, "--setenv", k, v)
	}
	for _, root := range jailRO {
		argv = append(argv, "--ro-bind", root, root)
	}
	argv = append(argv,
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--bind", p.Cwd, p.Cwd,
	)
	if p.KernelDir != "" {
		argv = append(argv, "--ro-bind", p.KernelDir, p.KernelDir)
	}
	argv = append(argv, "--ro-bind", p.Binary, p.Binary)
	argv = append(argv, "--bind", p.SockPath, p.SockPath)
	if p.StateDir != "" {
		argv = append(argv, "--bind", p.StateDir, filepath.Join(p.Cwd, ".rig-job", "sessions"))
	}
	visible := []string{p.Cwd, p.KernelDir}
	visible = append(visible, jailRO...)
	for i, entry := range p.Binds {
		src, rw, err := parseBind(entry, i)
		if err != nil {
			return nil, err
		}
		visible = append(visible, src)
		if rw {
			argv = append(argv, "--bind", src, src)
		} else {
			argv = append(argv, "--ro-bind", src, src)
		}
	}
	if root := venvRoot(p.Python); root != "" && !visibleUnder(root, visible) {
		argv = append(argv, "--ro-bind", root, root)
		visible = append(visible, root)
	}
	for _, bin := range []string{p.FFmpeg, p.Browser} {
		if root := binRoot(bin); root != "" && !visibleUnder(root, visible) {
			argv = append(argv, "--ro-bind", root, root)
			visible = append(visible, root)
		}
	}
	argv = append(argv,
		"--chdir", p.Cwd,
		"--",
		p.Binary, "-p", PromptStdin,
		"-base-url", p.BaseURL,
		"-model", p.Model,
	)
	if p.SessionID != "" {
		argv = append(argv, "-session-id", p.SessionID)
	}
	if p.Effort != "" {
		argv = append(argv, "-effort", p.Effort)
	}
	if p.Allow != "" {
		argv = append(argv, "-allow", p.Allow)
	}
	return argv, nil
}

func venvRoot(python string) string {
	if python == "" || !filepath.IsAbs(python) {
		return ""
	}
	root := filepath.Clean(filepath.Dir(filepath.Dir(python)))
	if _, err := os.Stat(filepath.Join(root, "pyvenv.cfg")); err != nil {
		return ""
	}
	return root
}

func binRoot(bin string) string {
	if bin == "" || !filepath.IsAbs(bin) {
		return ""
	}
	return filepath.Dir(bin)
}

func visibleUnder(root string, roots []string) bool {
	for _, r := range roots {
		if r != "" && (root == r || strings.HasPrefix(root, r+"/")) {
			return true
		}
	}
	return false
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
	if s == "jailed" || s == "landlock" || s == "off" {
		return s, nil
	}
	return "", fmt.Errorf("sandbox: expected \"jailed\", \"landlock\", or \"off\", got %q", s)
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

const jailPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

func modelEnv(opts RunOpts, model string) []string {
	row, ok := opts.modelRow(model)
	if !ok {
		return nil
	}
	env := []string{
		"RIG_MODEL_WINDOW=" + strconv.Itoa(row.Window),
		"RIG_MODEL_MAX_TOKENS=" + strconv.Itoa(row.MaxTokens),
		"RIG_MODEL_RESERVE=" + strconv.Itoa(row.Reserve),
		"RIG_MODEL_KEEP_RECENT=" + strconv.Itoa(row.KeepRecent),
	}
	if row.Retries != 0 {
		env = append(env, "RIG_MODEL_RETRIES="+strconv.Itoa(row.Retries))
	}
	if row.Remote {
		env = append(env, "RIG_MODEL_REMOTE=true")
	}
	return env
}

func jailSpawn(opts RunOpts, cwd, sockID string, workerCmd []string, model, effort, allow, sessionID string, fleet *fleetPipe, extraEnv ...string) ([]string, *SocketProxy, []string, string, error) {
	if runtime.GOOS != "linux" {
		return nil, nil, nil, PlatformRefusal(runtime.GOOS), nil
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, nil, nil, BwrapRefusal(), nil
	}
	kernelDir := ""
	if opts.RigHome != "" {
		kernelDir = filepath.Join(opts.RigHome, "kernel")
		if _, err := os.Stat(kernelDir); err != nil {
			return nil, nil, nil, KernelRefusal(kernelDir), nil
		}
	}

	scratch := filepath.Join(cwd, ".rig-job")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return nil, nil, nil, ScratchRefusal(scratch, err), nil
	}

	if opts.StateDir != "" {
		if err := os.MkdirAll(filepath.Join(scratch, "sessions"), 0o755); err != nil {
			return nil, nil, nil, ScratchRefusal(filepath.Join(scratch, "sessions"), err), nil
		}
	}

	sock := filepath.Join(scratch, sockID+".sock")
	proxy, err := NewSocketProxy(sock, opts.SwapURL)
	if err != nil {
		return nil, nil, nil, SocketRefusal(sock, err), nil
	}
	env := []string{
		"PATH=" + jailPath,
		"HOME=/tmp",
		"RIG_HOME=" + scratch,
	}
	env = append(env, modelEnv(opts, model)...)
	if opts.Python != "" {
		env = append(env, "RIG_PYTHON="+opts.Python)
	}
	if opts.FFmpeg != "" {
		env = append(env, "RIG_FFMPEG="+opts.FFmpeg)
	}
	if opts.Browser != "" {
		env = append(env, "RIG_BROWSER="+opts.Browser)
	}
	if fleet != nil {
		env = append(env, fleetEnv(fleet)...)
	}
	env = append(env, extraEnv...)
	argv, err := JailArgv(JailProfile{
		Bwrap:     bwrap,
		Binary:    workerCmd[0],
		BaseURL:   "unix:" + sock,
		Model:     model,
		Effort:    effort,
		SessionID: sessionID,
		Cwd:       cwd,
		KernelDir: kernelDir,
		SockPath:  sock,
		Binds:     opts.SandboxBinds,
		Python:    opts.Python,
		FFmpeg:    opts.FFmpeg,
		Browser:   opts.Browser,
		StateDir:  opts.StateDir,
		Allow:     allow,
		Env:       env,
	})
	if err != nil {
		proxy.Close()
		return nil, nil, nil, "", err
	}
	return argv, proxy, extraEnv, "", nil
}
