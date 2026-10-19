//go:build !linux

package stats

import "context"

func walk(ctx context.Context, s *Sample) {
	s.gpus = readGPUs(ctx)
}
