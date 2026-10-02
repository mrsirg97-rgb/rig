package openai

import (
	"net/http"
	"testing"
)

func TestHeaderTimeoutOffIsNoBoundAndZeroIsTheDefault(t *testing.T) {
	off, ok := NewWithConfig(Config{BaseURL: "http://x", Model: "m", HeaderTimeout: HeaderTimeoutOff}).(*provider)
	if !ok {
		t.Fatal("the provider type is fixed")
	}
	if got := off.client.Transport.(*http.Transport).ResponseHeaderTimeout; got != 0 {
		t.Fatalf("HeaderTimeoutOff must leave the transport unbounded, got %s", got)
	}
	def := NewWithConfig(Config{BaseURL: "http://x", Model: "m"}).(*provider)
	if got := def.client.Transport.(*http.Transport).ResponseHeaderTimeout; got != defaultHeaderTimeout {
		t.Fatalf("a zero config must take the default bound, got %s", got)
	}
}
