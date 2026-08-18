package tui

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
)

var updateGoldens = flag.Bool("update", false, "rewrite the golden stream files")

// scriptInput is the test's stdin: one byte at a time off a channel, so
// the feed is explicit and the reader's processing is observable.
type scriptInput struct {
	ch chan byte
}

func newScriptInput() *scriptInput { return &scriptInput{ch: make(chan byte)} }

func (s *scriptInput) Read(p []byte) (int, error) {
	b, ok := <-s.ch
	if !ok {
		return 0, io.EOF
	}
	p[0] = b
	return 1, nil
}

func (s *scriptInput) feed(text string) {
	for i := 0; i < len(text); i++ {
		s.ch <- text[i]
	}
}

func (s *scriptInput) close() { close(s.ch) }

// scriptedSession drives the Frontend the way the loop does: Input
// (delivered from a goroutine, the result awaited with a deadline),
// Notify for the turn's events, EOF at the end. The out buffer is the
// whole byte stream of the session — the golden's subject.
// lockBuf is the harness's out buffer: the frontend's goroutines write
// it and the test's goroutine reads it, so both sides lock.
type lockBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockBuf) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Bytes()
}

func (l *lockBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (l *lockBuf) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b.Reset()
}

type scriptedSession struct {
	t      *testing.T
	fe     *tui
	si     *scriptInput
	out    *lockBuf
	ctx    context.Context
	cancel context.CancelFunc
}

func newScriptedSession(t *testing.T, opts ...Option) *scriptedSession {
	t.Helper()
	out := &lockBuf{}
	si := newScriptInput()
	fe := New(si, out, opts...).(*tui)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		fe.Close()
		cancel()
	})
	return &scriptedSession{t: t, fe: fe, si: si, out: out, ctx: ctx, cancel: cancel}
}

// await blocks until the out buffer contains want (the reader and the
// draw are async; the test orders itself on the bytes).
func (s *scriptedSession) await(want string) {
	s.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !bytes.Contains(s.out.Bytes(), []byte(want)) {
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out awaiting %q in the stream:\n%s", want, s.out.String())
		}
		time.Sleep(time.Millisecond)
	}
}

// input starts one Input in a goroutine and awaits its result.
func (s *scriptedSession) input() (string, error) {
	s.t.Helper()
	ch := make(chan struct {
		line string
		err  error
	}, 1)
	go func() {
		l, err := s.fe.Input(s.ctx)
		ch <- struct {
			line string
			err  error
		}{l, err}
	}()
	select {
	case r := <-ch:
		return r.line, r.err
	case <-time.After(3 * time.Second):
		s.t.Fatalf("timed out in Input; stream so far:\n%s", s.out.String())
		return "", errors.New("timeout")
	}
}

// goldenStream is the named golden case (SPEC_TUI's testing section):
// a scripted event stream — a prompt, a reasoning and text stream, a
// tool round trip, a compaction, a turn end, a second prompt, EOF —
// the exact bytes.
func goldenStream(t *testing.T, th Theme, width int, news string) string {
	t.Helper()
	s := newScriptedSession(t,
		WithTheme(th),
		WithWidth(width),
		WithBanner(func(ctx context.Context) BannerIn {
			return BannerIn{
				Model: "huihui3.8", Effort: "xhigh", Used: 84300, Window: 262144,
				Up: 214000, Down: 18200, CacheRead: 187000,
			}
		}),
		WithNews(func(ctx context.Context) string { return news }),
		WithTicks(make(chan time.Time)), // a manual clock: the test is tick-free
	)

	// turn one.
	in := make(chan string, 1)
	go func() {
		line, err := s.input()
		if err != nil {
			s.t.Errorf("input one: %v", err)
		}
		in <- line
	}()
	s.await(th.Paint(SlotRule, strings.Repeat(th.Glyph(GlyphDot), width)))
	s.si.feed("hello\n")
	if line := <-in; line != "hello" {
		s.t.Fatalf("the first prompt = %q, want hello", line)
	}
	s.fe.Notify(core.ReasoningDelta{Text: "let me check"})
	s.fe.Notify(core.TextDelta{Text: "hel"})
	s.fe.Notify(core.TextDelta{Text: "lo\n"})
	s.fe.Notify(core.ToolStart{Call: core.ToolCall{
		Name: "bash",
		Args: []byte(`{"command":"go test ./x"}`),
	}})
	s.fe.Notify(core.ToolResult{
		Content:  "ok  \tx\t0.4s",
		Duration: 400 * time.Millisecond,
	})
	s.fe.Notify(core.TextDelta{Text: "done\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 3200, Completion: 136, CacheRead: 918}})
	s.fe.Notify(core.Compacted{
		Summary: "[compaction] the earlier turns, summarized",
		Dropped: 12000, Kept: 3400,
		Usage: core.Usage{Prompt: 2200, Completion: 300},
	})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

	// turn two, then EOF.
	in2 := make(chan string, 1)
	go func() {
		line, err := s.input()
		if err != nil {
			s.t.Errorf("input two: %v", err)
		}
		in2 <- line
	}()
	s.si.feed("bye\n")
	if line := <-in2; line != "bye" {
		s.t.Fatalf("the second prompt = %q, want bye", line)
	}
	s.si.close()
	line3, err3 := s.input()
	if !errors.Is(err3, io.EOF) {
		s.t.Fatalf("the third input = (%q, %v), want io.EOF", line3, err3)
	}
	return s.out.String()
}

func TestGoldenStream(t *testing.T) {
	oled, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	p1ascii, err := ResolveTheme("p1", []byte(`{"base":"p1","glyphs":"ascii"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		th    Theme
		width int
		file  string
	}{
		{"oled-50", oled, 50, "testdata/golden_oled_50.txt"},
		{"oled-100", oled, 100, "testdata/golden_oled_100.txt"},
		{"p1ascii-50", p1ascii, 50, "testdata/golden_p1ascii_50.txt"},
		{"p1ascii-100", p1ascii, 100, "testdata/golden_p1ascii_100.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := goldenStream(t, tc.th, tc.width, "")
			if *updateGoldens {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(tc.file, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("rewrote %s", tc.file)
				return
			}
			want, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatalf("read the golden: %v (run with -update)", err)
			}
			if got != string(want) {
				t.Fatalf("the stream drifted from the golden:\ngot:\n%q\nwant:\n%q", got, want)
			}
		})
	}
}

// TestGoldenStreamProtocol is the same stream through the escape-
// capture harness: the whole session's bytes must obey the live-region
// invariant (the cursor never reaches a committed row) and the escape
// vocabulary (text, LF, nA, nG, 2K, m — nothing else).
func TestGoldenStreamProtocol(t *testing.T) {
	oled, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	p1ascii, err := ResolveTheme("p1", []byte(`{"base":"p1","glyphs":"ascii"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		th    Theme
		width int
	}{{oled, 50}, {oled, 100}, {p1ascii, 50}, {p1ascii, 100}} {
		got := goldenStream(t, tc.th, tc.width, "")
		v := newVT(tc.width)
		v.feed([]byte(got))
		if v.err != "" {
			t.Fatalf("harness over the real stream: %s", v.err)
		}
	}
}
