package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type countingBG struct{ calls int }

func (b *countingBG) Submit(ctx context.Context, kind, name string, bound time.Duration, work func(ctx context.Context) (string, int)) (string, error) {
	b.calls++
	return "delegate: worker #1 started · run j1", nil
}

func TestAFetchsBoundAloneFetchesInTurnAndDeferSendsItToTheBackground(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("the page's words"))
	}))
	defer srv.Close()
	bg := &countingBG{}
	w := webTool(config{fetch: fetchConfig{Lookup: publicLookup, Do: direct()}, background: bg})

	body, err := execArgs(t, w, `{"action":"fetch","target":"`+srv.URL+`/","timeoutMs":5000}`)
	if err != nil || !strings.Contains(body, "the page's words") {
		t.Fatalf("a bound with no defer answers with the page: %q %v", body, err)
	}
	if bg.calls != 0 {
		t.Fatalf("a bound alone never goes to the background: %d", bg.calls)
	}

	body, err = execArgs(t, w, `{"action":"fetch","target":"`+srv.URL+`/","timeoutMs":5000,"defer":true}`)
	if err != nil || !strings.HasPrefix(body, "delegate: worker #1 started") {
		t.Fatalf("defer with a bound answers with the started line: %q %v", body, err)
	}
	if bg.calls != 1 {
		t.Fatalf("one background fetch: %d", bg.calls)
	}

	_, err = execArgs(t, w, `{"action":"fetch","target":"`+srv.URL+`/","defer":true}`)
	if err == nil || err.Error() != "web: defer needs timeoutMs, the bound a background run is killed at" {
		t.Fatalf("defer alone names what it needs: %v", err)
	}
	if bg.calls != 1 {
		t.Fatalf("a refused defer never goes to the background: %d", bg.calls)
	}
}
