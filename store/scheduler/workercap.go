package scheduler

import "context"

type WorkerCap chan struct{}

func NewWorkerCap(max int) WorkerCap {
	if max <= 0 {
		return nil
	}
	return make(WorkerCap, max)
}

func (c WorkerCap) TryHold() bool {
	if c == nil {
		return true
	}
	select {
	case c <- struct{}{}:
		return true
	default:
		return false
	}
}

func (c WorkerCap) Hold(ctx context.Context) error {
	if c == nil {
		return nil
	}
	select {
	case c <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c WorkerCap) Free() {
	if c != nil {
		<-c
	}
}
