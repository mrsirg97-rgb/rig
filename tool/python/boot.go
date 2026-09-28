package python

import (
	"bytes"
	"context"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/tool/execwrap"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

var (
	bootMu       sync.Mutex
	bootInflight *bootCall
)

type bootCall struct {
	done chan struct{}
	err  error
}

func ensureKernel(ctx context.Context) error {
	if _, err := os.Stat(defaultInterpreter()); err == nil {
		return nil
	}
	bootMu.Lock()
	if b := bootInflight; b != nil {
		bootMu.Unlock()
		<-b.done
		return b.err
	}
	b := &bootCall{done: make(chan struct{})}
	bootInflight = b
	bootMu.Unlock()

	venvDir := filepath.Join(homeDir(), ".pi", "agent", "kernel-venv")
	var err error
	if err = runStep(ctx, "python3", []string{"-m", "venv", venvDir}); err == nil {
		err = runStep(ctx, filepath.Join(venvDir, "bin", "pip"), []string{"install", "--quiet", "ipython", "numpy", "pandas"})
	}
	if err != nil {
		err = fmt.Errorf("kernel bootstrap failed (needs python3 + network): %v", err)
	}
	b.err = err
	bootMu.Lock()
	if bootInflight == b && err != nil {
		bootInflight = nil
	}
	bootMu.Unlock()
	close(b.done)
	return err
}

func runStep(ctx context.Context, command string, args []string) error {
	stepCtx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	argv := execwrap.Args(append([]string{command}, args...))
	cmd := exec.CommandContext(stepCtx, argv[0], argv[1:]...)
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	s := stderr.String()
	if s == "" {
		s = err.Error()
	}
	if len(s) > 500 {
		s = s[len(s)-500:]
	}
	first := ""
	if len(args) > 0 {
		first = args[0]
	}
	return fmt.Errorf("%s %s: %s", command, first, s)
}
