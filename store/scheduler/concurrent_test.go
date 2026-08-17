package scheduler_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

// Concurrent writers against one store file must serialize inside the
// lock window, not drop each other: an interactive session and a scheduler
// worker are two Opens of the same file. Ordering between true-parallel
// goroutines is not asserted (the database layer does not promise it);
// completeness is. The external seams are isolated per writer: this case
// probes the database-layer serialization, not shared-fixture races.

func TestConcurrentCreatesSerialize(t *testing.T) {
	h := newHarness(t, "/ws/cc")
	const (
		gor   = 10
		slice = 2
	)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for wi := 0; wi < 2; wi++ {
		for g := 0; g < gor; g++ {
			wg.Add(1)
			go func(wi, g int) {
				defer wg.Done()
				ct := newFakeCrontab("SHELL=/bin/bash\n")
				for i := 0; i < slice; i++ {
					_, err := schedCreate(context.Background(), h, ct,
						fmt.Sprintf("w%d-g%d-%d", wi, g, i))
					if err != nil {
						mu.Lock()
						errs = append(errs, err)
						mu.Unlock()
					}
				}
			}(wi, g)
		}
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("%d of %d concurrent creates failed: %v", len(errs), 2*gor*slice, errs[0])
	}
	var events, jobs int
	if err := h.st.Cwd.DB.QueryRow(`SELECT count(*) FROM events WHERE op = 'create'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := h.st.Cwd.DB.QueryRow(`SELECT count(*) FROM jobs WHERE state != 'removed'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if want := 2 * gor * slice; events != want {
		t.Fatalf("create events = %d, want %d (no lost writes)", events, want)
	}
	if want := 2 * gor * slice; jobs != want {
		t.Fatalf("live jobs = %d, want %d", jobs, want)
	}
	// the event spine stays strictly increasing
	var maxGap int64
	rows, err := h.st.Cwd.DB.Query(`SELECT seq FROM events WHERE op = 'create' ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var prev int64
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			t.Fatal(err)
		}
		if prev != 0 && seq < prev {
			t.Fatalf("seq not increasing: %d after %d", seq, prev)
		}
		if prev != 0 && seq-prev > maxGap {
			maxGap = seq - prev
		}
		prev = seq
	}
}

func schedCreate(ctx context.Context, h *harness, ct *fakeCrontab, name string) (string, error) {
	return sched.Create(ctx, h.st, ct, sched.CreateInput{
		Name: name, Prompt: "p", Cron: "0 0 * * *",
	}, h.sessCwd, "sess-x", runnerCmd, func() time.Time { return nowFixed })
}

func TestConcurrentRunRecordsSerialize(t *testing.T) {
	h := newHarness(t, "/ws/rr")
	if _, err := h.create(sched.CreateInput{Name: "busy", Prompt: "p", Cron: "0 8 * * *"}); err != nil {
		t.Fatal(err)
	}
	const (
		gor   = 10
		slice = 2
	)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for wi := 0; wi < 2; wi++ {
		for g := 0; g < gor; g++ {
			wg.Add(1)
			go func(wi, g int) {
				defer wg.Done()
				for i := 0; i < slice; i++ {
					if _, err := sched.RecordRun(context.Background(), h.st.Cwd, sched.RunRecordInput{
						ID: "j1", Status: "skip", Reason: fmt.Sprintf("w%d-g%d-%d", wi, g, i),
					}); err != nil {
						mu.Lock()
						errs = append(errs, err)
						mu.Unlock()
					}
				}
			}(wi, g)
		}
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("%d of %d concurrent records failed: %v", len(errs), 2*gor*slice, errs[0])
	}
	var events, runs int
	if err := h.st.Cwd.DB.QueryRow(`SELECT count(*) FROM events WHERE op = 'run'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := h.st.Cwd.DB.QueryRow(`SELECT count(*) FROM runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if want := 2 * gor * slice; events != want || runs != want {
		t.Fatalf("run events = %d, runs rows = %d, want %d (no lost writes)", events, runs, want)
	}
	// the jobs row's last_* reflects one of the writers, coherently set
	var lastStatus string
	var lastSet int64
	if err := h.st.Cwd.DB.QueryRow(`SELECT last_status, last_exit IS NOT NULL FROM jobs WHERE id = 'j1'`).Scan(&lastStatus, &lastSet); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(lastStatus, "skip") {
		t.Fatalf("last_status %q", lastStatus)
	}
}
