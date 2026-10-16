package wire_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v3/provider/wire"
)

func TestBackoffHonoursAndCapsRetryAfter(t *testing.T) {
	jitter := func() float64 { return 0 }
	base := 500 * time.Millisecond
	h := http.Header{}
	if got := wire.Backoff(base, jitter, 1, h); got != base {
		t.Fatalf("no retry-after: first attempt waits %s, want the base %s", got, base)
	}
	if got := wire.Backoff(base, jitter, 3, h); got != 4*base {
		t.Fatalf("no retry-after: third attempt waits %s, want %s (exponential)", got, 4*base)
	}
	h.Set("Retry-After", "2")
	if got := wire.Backoff(base, jitter, 3, h); got != 2*time.Second {
		t.Fatalf("an honourable retry-after waits %s, want the seconds asked for", got)
	}
	h.Set("Retry-After", "999999")
	if got := wire.Backoff(base, jitter, 1, h); got != wire.MaxRetryAfterWait {
		t.Fatalf("a hostile retry-after waits %s, want the cap %s", got, wire.MaxRetryAfterWait)
	}
	h.Set("Retry-After", "Infinity")
	if got := wire.Backoff(base, jitter, 1, h); got != wire.MaxRetryAfterWait {
		t.Fatalf("an overflowing retry-after waits %s, want the cap %s", got, wire.MaxRetryAfterWait)
	}
	h.Set("Retry-After", "-5")
	if got := wire.Backoff(base, jitter, 2, h); got != 2*base {
		t.Fatalf("a negative retry-after is no retry-after: waits %s, want the exponential %s", got, 2*base)
	}
}
