//go:build linux

package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	mfd, err := unix.Open("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx in this environment: %v", err)
	}
	zero := 0
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(mfd), uintptr(unix.TIOCSPTLCK),
		uintptr(unsafe.Pointer(&zero))); errno != 0 {
		t.Skipf("unlock the pty: %v", errno)
	}
	var minor int32
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(mfd), uintptr(unix.TIOCGPTN),
		uintptr(unsafe.Pointer(&minor))); errno != 0 {
		t.Skipf("name the pty: %v", errno)
	}
	sfd, err := unix.Open(fmt.Sprintf("/dev/pts/%d", minor), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open the pty slave: %v", err)
	}

	if err := unix.IoctlSetWinsize(mfd, unix.TIOCSWINSZ, &unix.Winsize{Row: 25, Col: 80}); err != nil {
		t.Skipf("set the pty size: %v", err)
	}
	master = os.NewFile(uintptr(mfd), "pty-master")
	slave = os.NewFile(uintptr(sfd), "pty-slave")
	t.Cleanup(func() {
		master.Close()
		slave.Close()
	})
	return master, slave
}

func ttyEcho(t *testing.T, f *os.File) bool {
	t.Helper()
	tc, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	if err != nil {
		t.Skipf("no termios on the pty: %v", err)
	}
	return tc.Iflag&unix.ECHO != 0
}

func TestPTYRawMode(t *testing.T) {
	_, slave := openPTY(t)
	tc, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Skipf("no termios on the pty: %v", err)
	}
	tc.Iflag |= unix.ECHO | unix.ICANON
	tc.Lflag |= unix.ECHO | unix.ICANON
	if err := unix.IoctlSetTermios(int(slave.Fd()), unix.TCSETS, tc); err != nil {
		t.Skipf("pin the pty state: %v", err)
	}

	out := &lockBuf{}
	fe := New(slave, out, oledTheme(t),
		WithTicks(make(chan time.Time))).(*tui)
	if ttyEcho(t, slave) {
		t.Fatal("raw mode did not clear the pty's echo")
	}
	fe.Close()
	if !ttyEcho(t, slave) {
		t.Fatal("Close did not restore the pty's echo")
	}
}

func TestPTYResize(t *testing.T) {
	master, slave := openPTY(t)
	th := oledTheme(t)
	winch := make(chan struct{}, 1)
	out := &lockBuf{}
	fe := New(slave, out, th,
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithWinch(winch), WithTicks(make(chan time.Time))).(*tui)
	defer fe.Close()

	go func() {
		fe.Input(context.Background())
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		fe.mu.Lock()
		started := fe.started
		fe.mu.Unlock()
		if started || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	fe.mu.Lock()
	w80 := fe.width
	fe.mu.Unlock()
	if w80 != 80 {
		t.Fatalf("the banner painted at width %d, want 80 (the pty's)", w80)
	}

	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 25, Col: 120}); err != nil {
		t.Fatalf("set the winsize: %v", err)
	}
	winch <- struct{}{}
	deadline = time.Now().Add(3 * time.Second)
	for {
		fe.mu.Lock()
		w := fe.width
		fe.mu.Unlock()
		if w == 120 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	fe.mu.Lock()
	w := fe.width
	fe.mu.Unlock()
	if w != 120 {
		t.Fatalf("the width after the resize = %d, want 120 (the terminal's)", w)
	}
}

func TestCloseStopsTheWinchSignal(t *testing.T) {
	master, slave := openPTY(t)
	defer master.Close()
	fe := New(slave, &lockBuf{}, oledTheme(t), WithTicks(make(chan time.Time))).(*tui)
	fe.mu.Lock()
	stop := fe.stopWinch
	fe.mu.Unlock()
	if stop == nil {
		t.Fatal("a terminal frontend must own the winch signal handler")
	}
	fe.Close()
	if fe.stopWinch != nil {
		t.Fatal("Close must stop the winch signal handler")
	}
}

// withStdinPTY puts the pty's slave on fd 0 for the test's lifetime, so
// the frontend sees a terminal on stdin — the shape the winch guard
// missed: stdin is fd 0, and the guard keyed on a nonzero fd.
func withStdinPTY(t *testing.T, fn func(master *os.File)) {
	t.Helper()
	master, slave := openPTY(t)
	orig, err := os.Open("/proc/self/fd/0")
	if err != nil {
		t.Skipf("save stdin: %v", err)
	}
	defer orig.Close()
	if err := unix.Dup2(int(slave.Fd()), 0); err != nil {
		t.Skipf("put the pty on stdin: %v", err)
	}
	defer func() {
		if err := unix.Dup2(int(orig.Fd()), 0); err != nil {
			t.Errorf("restore stdin: %v", err)
		}
	}()
	fn(master)
}

func setPTYSize(t *testing.T, f *os.File, w, h int) {
	t.Helper()
	if err := unix.IoctlSetWinsize(int(f.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(h), Col: uint16(w)}); err != nil {
		t.Fatalf("set the pty size %dx%d: %v", w, h, err)
	}
}

func awaitStream(t *testing.T, out *lockBuf, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !bytes.Contains(out.Bytes(), []byte(want)) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out awaiting %q in the stream:\n%s", want, out.String())
		}
		time.Sleep(time.Millisecond)
	}
}

func awaitStreamCount(t *testing.T, out *lockBuf, want string, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for bytes.Count(out.Bytes(), []byte(want)) < n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out awaiting %d× %q in the stream:\n%s", n, want, out.String())
		}
		time.Sleep(time.Millisecond)
	}
}

// feedStream replays the stream's frames into the harness. The pty's raw
// mode carries the paste-mode handshake, which is outside the harness's
// vocabulary: the frames keep the sync pair, the handshake is dropped.
func feedStream(v *vt, out *lockBuf, from int) int {
	chunks := out.writeChunks()
	for ; from < len(chunks); from++ {
		b := []byte(chunks[from])
		b = bytes.ReplaceAll(b, []byte(pasteOn), nil)
		b = bytes.ReplaceAll(b, []byte(pasteOff), nil)
		v.feed(b)
	}
	return from
}

const statusRow3 = "up 214k down 18k · cache r 187k 87%"

func assertStatusOnScreen(t *testing.T, label string, v *vt) {
	t.Helper()
	if v.err != "" {
		t.Fatalf("%s: harness: %s", label, v.err)
	}
	if v.clamped > 0 {
		t.Fatalf("%s: the protocol relied on %d cursor clamps", label, v.clamped)
	}
	visible := paintFree(strings.Join(v.rows, "\n"))
	for _, marker := range []string{"huihui3.8", "xhigh · default · auto", statusRow3} {
		if !strings.Contains(visible, marker) {
			t.Fatalf("%s: the status block is missing from the screen:\n%q", label, v.rows)
		}
	}
}

func assertTranscriptSurvives(t *testing.T, label string, v *vt, committed []string) {
	t.Helper()
	joined := strings.Join(v.hist, "\n") + "\n" + strings.Join(v.rows, "\n")
	pos := -1
	for _, c := range committed {
		idx := strings.Index(joined, c)
		if idx < 0 {
			t.Fatalf("%s: the committed row %q was overwritten:\nrows %q\nhist %q", label, c, v.rows, v.hist)
		}
		if idx <= pos {
			t.Fatalf("%s: the committed row %q is missing or out of order:\nrows %q\nhist %q", label, c, v.rows, v.hist)
		}
		pos = idx
	}
}

func TestStdinTerminalOwnsWinch(t *testing.T) {
	withStdinPTY(t, func(master *os.File) {
		fe := New(os.Stdin, &lockBuf{}, oledTheme(t), WithTicks(make(chan time.Time))).(*tui)
		defer fe.Close()
		fe.mu.Lock()
		stop := fe.stopWinch
		fe.mu.Unlock()
		if stop == nil {
			t.Fatal("stdin is the terminal: the frontend must own the winch signal handler")
		}
	})
}

// TestPTYWinchIdleRepaint: rig idle — no delta, no keystroke — the pane
// resizes, and the winch must repaint the status block anyway. The input
// is stdin, so the guard that keyed on a nonzero fd left this shape with
// no handler at all.
func TestPTYWinchIdleRepaint(t *testing.T) {
	th := oledTheme(t)
	withStdinPTY(t, func(master *os.File) {
		setPTYSize(t, master, 80, 44)
		out := &lockBuf{}
		fe := New(os.Stdin, out, th,
			WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
			WithTicks(make(chan time.Time))).(*tui)
		defer fe.Close()
		go fe.Input(context.Background())

		awaitStream(t, out, th.Paint(SlotDim, statusRow3))
		v := newVTScreen(80, 44)
		painted := feedStream(v, out, 0)

		setPTYSize(t, master, 80, 12)
		v = resizeVT(v, 80, 12)
		syscall.Kill(syscall.Getpid(), syscall.SIGWINCH)
		awaitStreamCount(t, out, th.Paint(SlotDim, statusRow3), 2)
		painted = feedStream(v, out, painted)
		assertStatusOnScreen(t, "idle winch", v)
	})
}

// TestPTYWinchParkedResize: the idle repro. One character parks the
// caret on the input row; the pane shrinks and tmux deletes the rows
// below the caret — the status rows — and nothing streams to repaint
// them. The winch must repaint the block after the shrink and after the
// regrow, from the parked aim, without overwriting the transcript.
func TestPTYWinchParkedResize(t *testing.T) {
	th := oledTheme(t)
	withStdinPTY(t, func(master *os.File) {
		setPTYSize(t, master, 80, 44)
		out := &lockBuf{}
		fe := New(os.Stdin, out, th,
			WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
			WithTicks(make(chan time.Time))).(*tui)
		defer fe.Close()
		go fe.Input(context.Background())

		awaitStream(t, out, th.Paint(SlotDim, statusRow3))
		// the shell prompt sits on the pane's bottom row when rig starts
		v := newVTScreen(80, 44)
		v.r, v.c, v.bottom = 43, 0, 43
		painted := feedStream(v, out, 0)

		if _, err := master.Write([]byte("x")); err != nil {
			t.Fatalf("write the keystroke: %v", err)
		}
		awaitStream(t, out, th.Paint(SlotText, " x"))
		painted = feedStream(v, out, painted)
		fe.mu.Lock()
		parked := fe.live.parked
		fe.mu.Unlock()
		if parked <= 0 {
			t.Fatalf("the keystroke left no park on the input row")
		}

		setPTYSize(t, master, 80, 12)
		v = resizeVT(v, 80, 12)
		visible := paintFree(strings.Join(v.rows, "\n"))
		if strings.Contains(visible, statusRow3) {
			t.Fatalf("the shrink kept the status rows: the repro needs them below the parked caret:\n%q", v.rows)
		}
		syscall.Kill(syscall.Getpid(), syscall.SIGWINCH)
		awaitStreamCount(t, out, th.Paint(SlotDim, statusRow3), 2)
		painted = feedStream(v, out, painted)
		assertStatusOnScreen(t, "after the shrink", v)
		assertTranscriptSurvives(t, "after the shrink", v, bannerRows(th))

		setPTYSize(t, master, 80, 44)
		v = resizeVT(v, 80, 44)
		syscall.Kill(syscall.Getpid(), syscall.SIGWINCH)
		awaitStreamCount(t, out, th.Paint(SlotDim, statusRow3), 3)
		painted = feedStream(v, out, painted)
		assertStatusOnScreen(t, "after the regrow", v)
		assertTranscriptSurvives(t, "after the regrow", v, bannerRows(th))
	})
}

func bannerRows(th Theme) []string {
	rows := strings.Split(RenderStatus(th, statusFixture()), "\n")
	if rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	for i := range rows {
		rows[i] = paintFree(rows[i])
	}
	return rows
}
