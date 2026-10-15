package scheduler_test

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v3/testenv"
)

func TestTestsNeverSeeTheOperatorHome(t *testing.T) {
	if os.Getenv("HOME") == testenv.OperatorHome {
		t.Fatalf("the suite must run in an isolated home, not %q", testenv.OperatorHome)
	}
}

func TestRealFetchRidesTheTestTransport(t *testing.T) {
	fetch := sched.RealFetch(0)
	_, err := fetch("http://127.0.0.1:8090/running")
	if err == nil || !strings.Contains(err.Error(), "testenv") {
		t.Fatalf("the real fetch must refuse a host that is not an httptest server, got %v", err)
	}
}

func TestRealFetchReadsAOKBodyVerbatim(t *testing.T) {
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[]}`)
	}))
	fetch := sched.RealFetch(time.Second)
	raw, err := fetch(srv.URL + "/v1/models")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if string(raw) != `{"data":[]}` {
		t.Fatalf("fetch = %s, want the body verbatim", raw)
	}
}

func TestRealFetchRefusesANonOKStatusByName(t *testing.T) {
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":"boom"}`)
	}))
	fetch := sched.RealFetch(time.Second)
	_, err := fetch(srv.URL + "/v1/models")
	if err == nil {
		t.Fatal("a 500 with a JSON body must fail closed, not parse the error page as the models table")
	}
	for _, needle := range []string{"500", srv.URL} {
		if !strings.Contains(err.Error(), needle) {
			t.Fatalf("the refusal must name %q, got %v", needle, err)
		}
	}
}

func TestRealFetchBoundsTheBody(t *testing.T) {
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(w, strings.NewReader(strings.Repeat("x", 4*1024*1024)))
	}))
	fetch := sched.RealFetch(time.Second)
	_, err := fetch(srv.URL + "/v1/models")
	if err == nil || !strings.Contains(err.Error(), "65536") {
		t.Fatalf("an oversized body must refuse naming the bound, got %v", err)
	}
}
