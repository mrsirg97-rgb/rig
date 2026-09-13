package scheduler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mrsirg97-rgb/rig/tool/execwrap"
)

const LandlockEnv = "RIG_LANDLOCK"

const landlockSpecVersion = 1

type LandlockSpec struct {
	V       int      `json:"v"`
	Cwd     string   `json:"cwd"`
	Scratch string   `json:"scratch"`
	Kernel  string   `json:"kernel,omitempty"`
	Binary  string   `json:"binary"`
	Binds   []string `json:"binds,omitempty"`
}

func LandlockPlatformRefusal(gos string) string {
	return "sandbox: the landlock profile is linux-only (profile landlock; this build is " + gos + ")"
}

func LandlockArchRefusal(goarch string) string {
	return "sandbox: the landlock profile needs amd64 or arm64 (profile landlock; this build is linux/" + goarch + ")"
}

func LandlockABIRefusal(abi int) string {
	return fmt.Sprintf("sandbox: landlock ABI %d is too old for the netless guarantee (need 4): install a newer kernel, use \"jailed\" (bwrap), or set sandbox \"off\"", abi)
}

func LandlockProbeRefusal(err error) string {
	return "sandbox: landlock is unavailable on this kernel (" + err.Error() + "): use \"jailed\" (bwrap), or set sandbox \"off\""
}

func LandlockBindRefusal(i int, path string, err error) string {
	return fmt.Sprintf("sandbox: sandboxBinds[%d]: %s: %v (a landlock grant must exist; use \"jailed\" or drop the bind)", i, path, err)
}

func LandlockABI() (int, error) {
	return landlockABIFn()
}

func landlockSpawn(opts RunOpts, cwd string, workerCmd []string, model, prompt, allow string, extraEnv ...string) ([]string, *SocketProxy, []string, string, error) {
	if runtime.GOOS != "linux" {
		return nil, nil, nil, LandlockPlatformRefusal(runtime.GOOS), nil
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return nil, nil, nil, LandlockArchRefusal(runtime.GOARCH), nil
	}
	probe := opts.LandlockABI
	if probe == nil {
		probe = LandlockABI
	}
	abi, err := probe()
	if err != nil {
		return nil, nil, nil, LandlockProbeRefusal(err), nil
	}
	if abi < 4 {
		return nil, nil, nil, LandlockABIRefusal(abi), nil
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
	tmp := filepath.Join(scratch, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return nil, nil, nil, ScratchRefusal(tmp, err), nil
	}
	for i, entry := range opts.SandboxBinds {
		src, _, err := parseBind(entry, i)
		if err != nil {
			return nil, nil, nil, "", err
		}
		if _, err := os.Stat(src); err != nil {
			return nil, nil, nil, LandlockBindRefusal(i, src, err), nil
		}
	}
	sock := filepath.Join(cwd, ".rig-job.sock")
	proxy, err := NewSocketProxy(sock, opts.SwapURL)
	if err != nil {
		return nil, nil, nil, SocketRefusal(sock, err), nil
	}
	spec := LandlockSpec{
		V: landlockSpecVersion, Cwd: cwd, Scratch: scratch,
		Kernel: kernelDir, Binary: workerCmd[0], Binds: opts.SandboxBinds,
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		proxy.Close()
		return nil, nil, nil, "", err
	}
	env := []string{
		"PATH=" + jailPath,
		"HOME=" + scratch,
		"RIG_HOME=" + scratch,
		"TMPDIR=" + tmp,
		LandlockEnv + "=" + string(raw),
		execwrap.Env + "=" + workerCmd[0],
	}
	env = append(env, extraEnv...)
	argv := append(append([]string{}, workerCmd...),
		"-p", prompt,
		"-base-url", "unix:"+sock,
		"-model", model)
	if allow != "" {
		argv = append(argv, "-allow", allow)
	}
	return argv, proxy, env, "", nil
}

func spawnJailed(opts RunOpts, profile, cwd string, workerCmd []string, model, prompt, allow string, extraEnv ...string) ([]string, *SocketProxy, []string, string, string, error) {
	if profile == "landlock" {
		argv, proxy, env, refuse, err := landlockSpawn(opts, cwd, workerCmd, model, prompt, allow, extraEnv...)
		return argv, proxy, env, "", refuse, err
	}
	argv, proxy, homeEnv, refuse, err := jailSpawn(opts, cwd, workerCmd, model, prompt, allow, extraEnv...)
	if err != nil || refuse != "" {
		return argv, proxy, nil, homeEnv, refuse, err
	}
	return argv, proxy, nil, homeEnv, "", nil
}

func ApplyLandlock(spec string) error {
	if spec == "" {
		return nil
	}
	var s LandlockSpec
	if err := json.Unmarshal([]byte(spec), &s); err != nil {
		return fmt.Errorf("landlock: profile: %w", err)
	}
	if s.V != landlockSpecVersion {
		return fmt.Errorf("landlock: profile: version %d: this binary knows version %d", s.V, landlockSpecVersion)
	}
	if err := landlockRestrictFn(s); err != nil {
		return err
	}
	runtime.LockOSThread()
	return nil
}
