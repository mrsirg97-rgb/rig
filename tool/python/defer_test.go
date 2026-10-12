package python

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type countingBG struct{ calls int }

func (b *countingBG) Submit(ctx context.Context, kind, name string, bound time.Duration, work func(ctx context.Context) (string, int)) (string, error) {
	b.calls++
	return "delegate: worker #1 started · run j1", nil
}

func TestACellsBoundAloneRunsInTurnAndDeferSendsItToTheBackground(t *testing.T) {
	py := pythonAvailable(t)
	host := filepath.Join(t.TempDir(), "fake-host.py")
	writeHost(t, host, fakePongHostSrc)
	bg := &countingBG{}
	kt := NewWith(py, host, bg)
	defer kt.Close()

	text, err := call(t, kt, map[string]any{"code": "print(6 * 7)", "timeoutMs": 20000})
	if err != nil || text != "fake-pong" {
		t.Fatalf("a bound with no defer answers with the cell's output: %q %v", text, err)
	}
	if bg.calls != 0 {
		t.Fatalf("a bound alone never goes to the background: %d", bg.calls)
	}

	text, err = call(t, kt, map[string]any{"code": "print(6 * 7)", "timeoutMs": 20000, "defer": true})
	if err != nil || !strings.HasPrefix(text, "delegate: worker #1 started") {
		t.Fatalf("defer with a bound answers with the started line: %q %v", text, err)
	}
	if bg.calls != 1 {
		t.Fatalf("one background run: %d", bg.calls)
	}

	_, err = call(t, kt, map[string]any{"code": "1", "defer": true})
	if err == nil || err.Error() != "python: defer needs timeoutMs, the bound a background run is killed at" {
		t.Fatalf("defer alone names what it needs: %v", err)
	}
	if bg.calls != 1 {
		t.Fatalf("a refused defer never goes to the background: %d", bg.calls)
	}
}
