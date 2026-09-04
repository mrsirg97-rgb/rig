package openai

import (
	"net/http"
	"testing"
	"time"
)

func TestThePlainTransportKeepsTheDefaultBounds(t *testing.T) {
	p := New("http://example.invalid", "local").(*provider)
	tr, ok := p.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", p.client.Transport)
	}
	if tr.DialContext == nil {
		t.Fatal("the dial must stay bounded by DefaultTransport's dialer")
	}
	if tr.TLSHandshakeTimeout != 10*time.Second {
		t.Fatalf("TLSHandshakeTimeout = %v, want DefaultTransport's 10s", tr.TLSHandshakeTimeout)
	}
	if tr.Proxy == nil {
		t.Fatal("the proxy-from-environment default must survive")
	}
	if tr.ResponseHeaderTimeout != defaultHeaderTimeout {
		t.Fatalf("ResponseHeaderTimeout = %v, want %v", tr.ResponseHeaderTimeout, defaultHeaderTimeout)
	}
}
