package tui

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPTY gives the test a pty pair (a master to drive and observe, a
// slave to hand to the frontend). The pty is opened the Linux way
// (/dev/ptmx, grant, unlock, the slave's name); every failure is a
// clean skip — the pty-dependent cases run where a pty exists and are
// absent where it does not (SPEC_TUI's testing section).
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
	// the pty starts at the kernel default; pin the size this test needs.
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

// ttyEcho reports the terminal's echo bit (raw mode clears it; the
// restore puts it back).
func ttyEcho(t *testing.T, f *os.File) bool {
	t.Helper()
	tc, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	if err != nil {
		t.Skipf("no termios on the pty: %v", err)
	}
	return tc.Iflag&unix.ECHO != 0
}

// TestPTYRawMode is the named pty case: when the input is a terminal,
// the frontend puts it in raw mode (x/term) and Close restores the
// mode — the operator's terminal is left as it was. The pty's start
// state is pinned first (this kernel's ptys start echo-off); the
// assertion is the change, not the default.
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
	fe := New(slave, out, WithTheme(oledTheme(t)),
		WithTicks(make(chan time.Time))).(*tui)
	if ttyEcho(t, slave) {
		t.Fatal("raw mode did not clear the pty's echo")
	}
	fe.Close()
	if !ttyEcho(t, slave) {
		t.Fatal("Close did not restore the pty's echo")
	}
}

// TestPTYResize is the named pty case: a resize takes the new width
// from the terminal (the production path) and repaints the live
// region at it — history is the terminal's problem (decision 2).
func TestPTYResize(t *testing.T) {
	master, slave := openPTY(t)
	th := oledTheme(t)
	winch := make(chan struct{}, 1)
	out := &lockBuf{}
	fe := New(slave, out, WithTheme(th),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithWinch(winch), WithTicks(make(chan time.Time))).(*tui)
	defer fe.Close()

	// the first banner paints at the pty's width (80): start one
	// Input (the banner commits there) and let it block.
	go func() {
		fe.Input(context.Background())
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		fe.mu.Lock()
		bannered := fe.bannered
		fe.mu.Unlock()
		if bannered || time.Now().After(deadline) {
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

	// the resize: the new size lands on the terminal, the winch fires,
	// and the width follows the terminal.
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
