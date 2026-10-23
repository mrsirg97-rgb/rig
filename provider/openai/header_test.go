package openai

import (
	openaiwire "github.com/mrsirg97-rgb/rig/v3/provider/wire"
	"net/http"
	"testing"
)

func TestHeaderTimeoutOffIsNoBoundAndZeroIsTheDefault(t *testing.T) {
	off, ok := New("http://x", "m", WithHeaderTimeout(openaiwire.HeaderTimeoutOff)).(*provider)
	if !ok {
		t.Fatal("the provider type is fixed")
	}
	if got := off.client.Transport.(*http.Transport).ResponseHeaderTimeout; got != 0 {
		t.Fatalf("openaiwire.HeaderTimeoutOff must leave the transport unbounded, got %s", got)
	}
	def := New("http://x", "m").(*provider)
	if got := def.client.Transport.(*http.Transport).ResponseHeaderTimeout; got != defaultHeaderTimeout {
		t.Fatalf("a zero config must take the default bound, got %s", got)
	}
}
