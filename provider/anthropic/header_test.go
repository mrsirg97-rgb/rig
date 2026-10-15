package anthropic

import (
	"net/http"
	"testing"
	"time"
)

func TestHeaderTimeoutOffIsNoBoundAndZeroIsTheDefault(t *testing.T) {
	off, ok := New(Config{BaseURL: "http://x", Model: "m", HeaderTimeout: HeaderTimeoutOff}).(*provider)
	if !ok {
		t.Fatal("the provider type is fixed")
	}
	if got := off.client.Transport.(*http.Transport).ResponseHeaderTimeout; got != 0 {
		t.Fatalf("HeaderTimeoutOff must leave the transport unbounded, got %s", got)
	}
	def := New(Config{BaseURL: "http://x", Model: "m"}).(*provider)
	if got := def.client.Transport.(*http.Transport).ResponseHeaderTimeout; got != defaultHeaderTimeout {
		t.Fatalf("a zero config must take the five-minute bound, got %s", got)
	}
}

func TestDefaults(t *testing.T) {
	p := New(Config{Model: "m"}).(*provider)
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
	p := New(Config{Model: "m", RetryBase: time.Millisecond}).(*provider)
	h := http.Header{}
	h.Set("Retry-After", "999999")
	if wait := p.backoff(1, h); wait != defaultHeaderTimeout {
		t.Fatalf("a hostile retry-after waits %s, want the header bound's cap %s", wait, defaultHeaderTimeout)
	}
	h.Set("Retry-After", "2")
	if wait := p.backoff(1, h); wait != 2*time.Second {
		t.Fatalf("an honourable retry-after waits %s, want the seconds asked for", wait)
	}
}
