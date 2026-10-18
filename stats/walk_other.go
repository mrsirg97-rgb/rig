//go:build !linux

package stats

import "context"

// walk reads what a non-Linux box states: the cards, when nvidia-smi is on
// it. The /proc and /sys sources are Linux's and render nothing here.
func walk(ctx context.Context, s *Sample) {
	s.gpus = readGPUs(ctx)
}
