package stats

import (
	"context"
	"sync"
	"testing"
	"time"
)

func vitals(at, total, idle, rx uint64) *Sample {
	load := [3]float64{1, 2, 3}
	return &Sample{
		at:     time.Unix(int64(at), 0),
		uptime: float64(at),
		load:   &load,
		cpu:    &counters{cores: 4, total: total, idle: idle},
		links:  []linkCounters{{iface: "eno1", rxBytes: rx, txBytes: rx / 2}},
	}
}

func script(t *testing.T, samples ...*Sample) *Sampler {
	t.Helper()
	i := 0
	return NewSampler(func(context.Context) *Sample {
		if i >= len(samples) {
			i = len(samples) - 1
		}
		cur := samples[i]
		i++
		return cur
	})
}

func TestTheFirstReadAnswersNoRateAndTheSecondDoes(t *testing.T) {
	s := script(t, vitals(100, 100, 50, 1000), vitals(102, 200, 100, 2000))
	first := s.Report(context.Background())
	if first.CPU == nil || first.CPU.Cores != 4 {
		t.Fatalf("the core count answers at once: %+v", first.CPU)
	}
	if first.CPU.Percent != nil {
		t.Fatalf("one sample states no busy share: %v", *first.CPU.Percent)
	}
	if len(first.Links) != 1 || first.Links[0].RxRate != nil {
		t.Fatalf("one sample states no rate: %+v", first.Links)
	}
	second := s.Report(context.Background())
	f64(t, second.CPU.Percent, 50)
	f64(t, second.Links[0].RxRate, 500)
	f64(t, second.Links[0].TxRate, 250)
}

func TestASampleOlderThanTheHeldOneNeverBecomesTheWindow(t *testing.T) {
	s := script(t,
		vitals(102, 200, 100, 2000),
		vitals(100, 100, 50, 1000),
		vitals(104, 300, 150, 3000))
	s.Report(context.Background())
	stale := s.Report(context.Background())
	if stale.CPU.Percent != nil {
		t.Fatalf("an older read answers no busy share: %v", *stale.CPU.Percent)
	}
	if stale.Links[0].RxRate != nil {
		t.Fatalf("an older read answers no rate: %v", *stale.Links[0].RxRate)
	}
	fresh := s.Report(context.Background())
	f64(t, fresh.CPU.Percent, 50)
	f64(t, fresh.Links[0].RxRate, 500)
}

func TestAReadPricedAtOneSecondAnswersTheRatesOfItsOwnWindow(t *testing.T) {
	now := time.Now()
	cur := vitals(100, 100, 50, 1000)
	cur.at = now
	old := vitals(98, 100, 50, 1000)
	old.at = now.Add(-time.Second)
	r := cur.Report(old)
	f64(t, r.Links[0].RxRate, 0)
	if r.CPU.Percent != nil {
		t.Fatalf("no jiffies crossed answers no share: %v", *r.CPU.Percent)
	}
}

func TestASecondReaderDoesNotWaitForTheFirstsSubprocess(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	inside := make(chan struct{})
	blocked := make(chan struct{})
	s := NewSampler(func(context.Context) *Sample {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			close(inside)
			<-blocked
		} else {
			close(blocked)
		}
		return vitals(100, 100, 50, 1000)
	})
	go func() { s.Report(context.Background()) }()
	<-inside
	returned := make(chan Report, 1)
	go func() { returned <- s.Report(context.Background()) }()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("a reader waited on another reader's subprocess: the lock is held across the read")
	}
}

func TestAReusedSamplerAnswersEveryRead(t *testing.T) {
	s := script(t, vitals(100, 100, 50, 1000), vitals(102, 200, 100, 2000))
	for i := 0; i < 3; i++ {
		r := s.Report(context.Background())
		if r.CPU == nil || r.Load == nil {
			t.Fatalf("read %d answers the absolutes: %+v", i+1, r.CPU)
		}
	}
}

func TestARacedSamplerPairsConsecutiveSamples(t *testing.T) {
	var mu sync.Mutex
	i := 0
	s := NewSampler(func(context.Context) *Sample {
		mu.Lock()
		defer mu.Unlock()
		cur := i
		i++
		return vitals(100+uint64(cur)*2, 100+uint64(cur)*100, 50+uint64(cur)*50, 1000*uint64(cur+1))
	})
	var wg sync.WaitGroup
	for k := 0; k < 8; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 4; n++ {
				r := s.Report(context.Background())
				if r.CPU == nil {
					t.Error("every read answers the core count")
					continue
				}
				if p := r.CPU.Percent; p != nil && (*p < 0 || *p > 100) {
					t.Errorf("the busy share is a share: %v", *p)
				}
				if l := r.Links[0].RxRate; l != nil && *l < 0 {
					t.Errorf("a rate against the held sample never runs negative: %v", *l)
				}
			}
		}()
	}
	wg.Wait()
}
