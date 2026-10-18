package stats

import (
	"context"
	"sync"
)

type Sampler struct {
	mu   sync.Mutex
	prev *Sample
	read func(context.Context) *Sample
}

func NewSampler(read func(context.Context) *Sample) *Sampler {
	if read == nil {
		read = Read
	}
	return &Sampler{read: read}
}

func (s *Sampler) Report(ctx context.Context) Report {
	cur := s.read(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	r := cur.Report(s.prev)
	if s.prev == nil || cur.at.After(s.prev.at) {
		s.prev = cur
	}
	return r
}
