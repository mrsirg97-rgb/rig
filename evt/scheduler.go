package evt

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrNoEngine = errors.New("evt: scheduler: no engine")
	ErrStarted  = errors.New("evt: scheduler: the loop is already started")
)

type Scheduler interface {
	Start() error
	Schedule(c Closure, priority int) uint64
	Stop()
	Done() <-chan struct{}
}

type scheduler struct {
	mu      sync.Mutex
	engine  Engine
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
}

func NewScheduler(e Engine) Scheduler {
	closed := make(chan struct{})
	close(closed)
	return &scheduler{engine: e, done: closed}
}

func (s *scheduler) Start() error {
	if s.engine == nil {
		return ErrNoEngine
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return ErrStarted
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.done, s.started = cancel, done, true
	go func() {
		s.engine.Start(ctx)
		close(done)
	}()
	return nil
}

func (s *scheduler) Schedule(c Closure, priority int) uint64 {
	if s.engine == nil {
		return 0
	}
	return s.engine.Add(c, priority)
}

func (s *scheduler) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	cancel, done := s.cancel, s.done
	s.started = false
	s.mu.Unlock()
	cancel()
	s.engine.Stop()
	<-done
}

func (s *scheduler) Done() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}
