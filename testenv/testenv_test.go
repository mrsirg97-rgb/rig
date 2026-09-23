package testenv_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/testenv"
)

func TestTransportRefusesANonServerHost(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8090/running", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = testenv.Transport().RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1:8090") {
		t.Fatalf("a host that is not an httptest server must be refused, got %v", err)
	}
}

func TestTransportAllowsAServerHost(t *testing.T) {
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := testenv.Transport().RoundTrip(req)
	if err != nil {
		t.Fatalf("a registered httptest server must be dialable: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("the server body = %q, want ok", body)
	}
}
