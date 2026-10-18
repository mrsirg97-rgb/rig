package stats

import (
	"context"
	"sync"
)

// A Sampler answers one Report per read, holding the last sample so the
// rates have a window. It is safe for concurrent reads and owns no timer, no
// goroutine, and no cache: the dashboard's poll is its clock, so a closed
// page samples nothing.
type Sampler struct {
	mu   sync.Mutex
	prev *Sample
	read func(context.Context) *Sample
}

// NewSampler builds a sampler over a read of the world. A nil read is Read
// itself: the caller names a source only to replace it.
func NewSampler(read func(context.Context) *Sample) *Sampler {
	if read == nil {
		read = Read
	}
	return &Sampler{read: read}
}

// Report samples the world and folds it against the last sample. Two reads
// race safely: the lock serializes the swap, so every report's window is a
// pair of consecutive samples, never a pair of unrelated ones.
func (s *Sampler) Report(ctx context.Context) Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.read(ctx)
	r := cur.Report(s.prev)
	s.prev = cur
	return r
}
