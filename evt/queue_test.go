package evt_test

import (
	"math/rand"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/evt"
)

func ev(id uint64, priority int) evt.Event { return evt.NewEvent(id, priority, nil) }

func TestQueueBasic(t *testing.T) {
	q := evt.NewQueue(4)
	q.Push(ev(100, 3))
	q.Push(ev(90, 5))
	q.Push(ev(80, 5))
	q.Push(ev(50, 1))
	q.Push(ev(60, 4))
	wantPri := []int{5, 5, 4, 3, 1}
	wantID := []uint64{80, 90, 60, 100, 50}
	for i := range wantPri {
		e, ok := q.Pop()
		if !ok {
			t.Fatalf("pop %d: empty", i)
		}
		if e.Priority() != wantPri[i] || e.ID() != wantID[i] {
			t.Fatalf("pop %d: (%d,%d), want (%d,%d)", i, e.Priority(), e.ID(), wantPri[i], wantID[i])
		}
	}
	if q.Len() != 0 {
		t.Fatalf("len %d after draining", q.Len())
	}
}

func TestQueueEmptyPop(t *testing.T) {
	q := evt.NewQueue(0)
	if e, ok := q.Pop(); ok || e != nil {
		t.Fatalf("pop on empty: (%v, %v)", e, ok)
	}
	if e, ok := q.Peek(); ok || e != nil {
		t.Fatalf("peek on empty: (%v, %v)", e, ok)
	}
	if v := q.View(); len(v) != 0 {
		t.Fatalf("view on empty: %d", len(v))
	}
}

func TestQueueMultithread(t *testing.T) {
	const threads, per = 4, 2000
	q := evt.NewQueue(64)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for th := 0; th < threads; th++ {
		wg.Add(1)
		go func(th int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				e := ev(uint64(th*per+i), i%10)
				mu.Lock()
				q.Push(e)
				mu.Unlock()
			}
		}(th)
	}
	wg.Wait()
	if q.Len() != threads*per {
		t.Fatalf("len %d, want %d", q.Len(), threads*per)
	}
	assertSortedDrain(t, q, threads*per)
}

func TestQueueStress(t *testing.T) {
	const n = 100000
	r := rand.New(rand.NewSource(1))
	q := evt.NewQueue(8)
	var id uint64
	pushed, popped := 0, 0
	for i := 0; i < n; i++ {
		if r.Intn(3) == 0 {
			if _, ok := q.Pop(); ok {
				popped++
			}
			continue
		}
		id++
		q.Push(ev(id, r.Intn(50)))
		pushed++
		if r.Intn(10) == 0 {
			if !q.Update(id, r.Intn(50)) {
				t.Fatalf("update of a live id %d reported absent", id)
			}
		}
	}
	if q.Len() != pushed-popped {
		t.Fatalf("len %d, want %d", q.Len(), pushed-popped)
	}
	assertSortedDrain(t, q, pushed-popped)
}

func assertSortedDrain(t *testing.T, q evt.Queue, want int) {
	t.Helper()
	var prev evt.Event
	count := 0
	for {
		e, ok := q.Pop()
		if !ok {
			break
		}
		count++
		if prev != nil {
			if e.Priority() > prev.Priority() {
				t.Fatalf("priority rose: %d after %d", e.Priority(), prev.Priority())
			}
			if e.Priority() == prev.Priority() && e.ID() < prev.ID() {
				t.Fatalf("id fell within priority %d: %d after %d", e.Priority(), e.ID(), prev.ID())
			}
		}
		prev = e
	}
	if count != want {
		t.Fatalf("drained %d, want %d", count, want)
	}
}

func BenchmarkQueuePushPop(b *testing.B) {
	q := evt.NewQueue(1024)
	r := rand.New(rand.NewSource(1))
	for i := 0; i < b.N; i++ {
		q.Push(ev(uint64(i), r.Intn(10)))
		if i%2 == 1 {
			q.Pop()
		}
	}
}

func TestQueueUpdateReprioritizes(t *testing.T) {
	q := evt.NewQueue(4)
	q.Push(ev(1, 5))
	q.Push(ev(2, 3))
	q.Push(ev(3, 1))
	if !q.Update(3, 9) {
		t.Fatal("update of a pending id must succeed")
	}
	if e, _ := q.Peek(); e.ID() != 3 || e.Priority() != 9 {
		t.Fatalf("peek after update: (%d,%d), want (3,9)", e.ID(), e.Priority())
	}
	if q.Update(42, 1) {
		t.Fatal("update of an unknown id must be false")
	}
	q.Pop()
	if q.Update(3, 1) {
		t.Fatal("update of a popped id must be false")
	}
}

func TestQueueViewIsASortedSnapshot(t *testing.T) {
	q := evt.NewQueue(4)
	for i, p := range []int{2, 9, 4, 9, 1} {
		q.Push(ev(uint64(i+1), p))
	}
	v := q.View()
	want := []uint64{2, 4, 3, 1, 5}
	for i, e := range v {
		if e.ID() != want[i] {
			t.Fatalf("view[%d] = %d, want %d", i, e.ID(), want[i])
		}
	}
	if q.Len() != 5 {
		t.Fatalf("view must not drain the queue: len %d", q.Len())
	}
	if e, _ := q.Peek(); e.ID() != 2 {
		t.Fatalf("peek after view: %d, want 2", e.ID())
	}
}

func TestQueueDuplicateIDIsIgnored(t *testing.T) {
	q := evt.NewQueue(4)
	q.Push(ev(7, 1))
	q.Push(ev(7, 9))
	if q.Len() != 1 {
		t.Fatalf("len %d, want 1", q.Len())
	}
	if e, _ := q.Peek(); e.Priority() != 1 {
		t.Fatalf("the first push wins: priority %d", e.Priority())
	}
}
