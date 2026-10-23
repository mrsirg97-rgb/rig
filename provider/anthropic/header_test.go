package anthropic

import (
	"net/http"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v3/provider/wire"
)

func TestHeaderTimeoutOffIsNoBoundAndZeroIsTheDefault(t *testing.T) {
	off, ok := New("m", WithBaseURL("http://x"), WithHeaderTimeout(wire.HeaderTimeoutOff)).(*provider)
	if !ok {
		t.Fatal("the provider type is fixed")
	}
	if got := off.client.Transport.(*http.Transport).ResponseHeaderTimeout; got != 0 {
		t.Fatalf("HeaderTimeoutOff must leave the transport unbounded, got %s", got)
	}
	def := New("m", WithBaseURL("http://x")).(*provider)
	if got := def.client.Transport.(*http.Transport).ResponseHeaderTimeout; got != wire.DefaultHeaderTimeout {
		t.Fatalf("a zero config must take the five-minute bound, got %s", got)
	}
}

func TestDefaults(t *testing.T) {
	p := New("m").(*provider)
	if p.baseURL != defaultBaseURL {
		t.Fatalf("baseURL = %q, want the api default", p.baseURL)
	}
	if p.endpoint() != defaultBaseURL+"/v1/messages" {
		t.Fatalf("endpoint = %q", p.endpoint())
	}
	if p.version != defaultVersion {
		t.Fatalf("version = %q, want the dated default", p.version)
	}
	if p.idle != defaultIdleTimeout {
		t.Fatalf("idle = %s, want the idle bound", p.idle)
	}
}

func TestBackoffCapsAHostileRetryAfterAtTheHeaderBound(t *testing.T) {
	p := New("m", WithRetryBase(time.Millisecond)).(*provider)
	h := http.Header{}
	h.Set("Retry-After", "999999")
	if wait := wire.Backoff(p.base, p.jitter, 1, h); wait != wire.DefaultHeaderTimeout {
		t.Fatalf("a hostile retry-after waits %s, want the header bound's cap %s", wait, wire.DefaultHeaderTimeout)
	}
	h.Set("Retry-After", "2")
	if wait := wire.Backoff(p.base, p.jitter, 1, h); wait != 2*time.Second {
		t.Fatalf("an honourable retry-after waits %s, want the seconds asked for", wait)
	}
}

func TestBackoffClampsAnOverflowingRetryAfterToTheHeaderBound(t *testing.T) {
	p := New("m", WithRetryBase(time.Millisecond)).(*provider)
	for _, v := range []string{"1e10", "infinity"} {
		h := http.Header{}
		h.Set("Retry-After", v)
		wait := wire.Backoff(p.base, p.jitter, 1, h)
		if wait <= 0 {
			t.Fatalf("retry-after %q waits %s; the conversion overflowed the cap comparison", v, wait)
		}
		if wait != wire.MaxRetryAfterWait {
			t.Fatalf("retry-after %q waits %s, want the cap %s", v, wait, wire.MaxRetryAfterWait)
		}
	}
}
