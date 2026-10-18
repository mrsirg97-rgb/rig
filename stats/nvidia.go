package stats

import (
	"context"
	"os/exec"
)

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
