package stats

import (
	"context"
	"os/exec"
)

// readGPUs asks nvidia-smi. A box with no binary, no driver, or a driver
// busy past gpuTimeout answers no cards.
func readGPUs(ctx context.Context) []GPU {
	cctx, cancel := context.WithTimeout(ctx, gpuTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "nvidia-smi",
		"--query-gpu="+nvidiaFields, "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	return parseNvidiaCSV(string(out))
}
