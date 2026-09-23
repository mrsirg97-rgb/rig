package swarm_test

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/swarm"
)

// recorder is the root's r.rec stand-in: the controller resolves the
// frontend on every notify, so the recorder can appear after wiring and
// be swapped by /new and /resume.
type recorder struct {
	mu sync.Mutex
	fe core.Frontend
}

func (r *recorder) resolve() core.Frontend {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fe
}

func (r *recorder) set(fe core.Frontend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fe = fe
}

func TestSwarmFrontendResolvesWhenTheRecorderAppears(t *testing.T) {
	rec := &recorder{}
	h := newHarnessResolved(t, rec.resolve)
	h.create(t, "do the work")
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	// No recorder at wiring time: the drain must emit safely instead of
	// panicking on a typed-nil frontend.
	h.waitFor(t, "the task in review with no recorder", func() bool {
		return h.status(t, "t1") == "review"
	})
	got := &recordFrontend{}
	rec.set(got)
	// A later start routes its frames to the now-present recorder.
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	notices := waitForNotices(t, got, 1)
	if notices[0] != "swarm: the board emptied — all workers exited" {
		t.Fatalf("the notice must land in the recorder once it exists, got %q", notices[0])
	}
}

func TestSwarmSessionSwapRoutesNoticesToTheNewRecorder(t *testing.T) {
	rec := &recorder{}
	first := &recordFrontend{}
	rec.set(first)
	h := newHarnessResolved(t, rec.resolve)
	h.create(t, "do the work")
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	notices := waitForNotices(t, first, 1)
	if notices[0] != "swarm: the board emptied — all workers exited" {
		t.Fatalf("the first recorder got %q", notices[0])
	}

	second := &recordFrontend{}
	rec.set(second)
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	notices = waitForNotices(t, second, 1)
	if notices[0] != "swarm: the board emptied — all workers exited" {
		t.Fatalf("the swap must route to the new recorder, got %q", notices[0])
	}
	if got := len(first.notices()); got != 1 {
		t.Fatalf("the old recorder must not get the post-swap frames, got %d", got)
	}
}

type panicFrontend struct{}

func (panicFrontend) Input(ctx context.Context) (string, error) { return "", io.EOF }
func (panicFrontend) Notify(ev core.Event)                      { panic("frontend exploded") }

func TestSwarmPanickingFrontendDoesNotKillTheDrainLoop(t *testing.T) {
	h := newHarnessResolved(t, func() core.Frontend { return panicFrontend{} })
	h.create(t, "do the work")

	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w

	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.waitFor(t, "the task in review", func() bool {
		return h.status(t, "t1") == "review"
	})
	h.waitFor(t, "the worker exited", func() bool {
		rows := h.ctl.List()
		return len(rows) == 1 && rows[0].State == "exited"
	})
	// Stop waits for the drain workers, so every emit has run when it
	// returns; its own frames panic too and must land loud.
	if _, err := h.ctl.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), "recovered from panic") {
		t.Fatalf("the panic must land loud on stderr, got %q", out)
	}
	if !strings.Contains(string(out), "frontend exploded") {
		t.Fatalf("the loud line must name the panic, got %q", out)
	}
}
