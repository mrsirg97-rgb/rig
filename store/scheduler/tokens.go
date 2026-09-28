package scheduler

import (
	"context"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/models"
	"os"
	"time"
)

func acquireRowTokens(ctx context.Context, home, model string, n int) (*os.File, error) {
	if n <= 0 {
		n = 1
	}
	for {
		for i := 0; i < n; i++ {
			fd, held, err := acquireLock(home, fmt.Sprintf("delegate:model:%s:%d", model, i))
			if err != nil {
				return nil, fmt.Errorf("concurrency: lock: %w", err)
			}
			if held {
				return fd, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("concurrency: the row's %d tokens are all held (model %s)", n, model)
		case <-time.After(slotPollInterval):
		}
	}
}

func (opts RunOpts) modelRow(model string) (models.Model, bool) {
	if opts.Models == nil {
		return models.Model{}, false
	}
	row, ok := opts.Models().Get(model)
	return row, ok
}
