package python

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestInterruptTearsDownTheBusyKernel(t *testing.T) {
	py := requireKernel(t)
	kt := NewWith(py, DefaultHost())
	defer kt.Close()

	seed, err := call(t, kt, map[string]any{"code": "seed = 1", "timeoutMs": 5000})
	if err != nil {
		t.Fatalf("seed failed: %s (%v)", seed, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	payload, _ := json.Marshal(map[string]any{"code": "import time; time.sleep(30)", "timeoutMs": 60000})
	done := make(chan struct{})
	var interrupted string
	var interruptedErr error
	go func() {
		interrupted, interruptedErr = kt.Exec(ctx, payload)
		close(done)
	}()

	time.Sleep(500 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the interrupted call did not return")
	}
	if interruptedErr == nil || !strings.Contains(interruptedErr.Error(), "context canceled") {
		t.Fatalf("the interrupted call must surface the cancel: %q (%v)", interrupted, interruptedErr)
	}

	start := time.Now()
	text, err := call(t, kt, map[string]any{"code": "1 + 1", "timeoutMs": 5000})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("the call after an interrupt must not wait behind the busy kernel: %s (%v)", text, err)
	}
	if !strings.Contains(text, "2") {
		t.Fatalf("the fresh kernel must answer: %s", text)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("the call after an interrupt took %v; the busy kernel was not torn down", elapsed)
	}
}
