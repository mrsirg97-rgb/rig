package tui

import (
	"syscall"
	"testing"
	"time"
)

func TestWinchStopEndsSignalDelivery(t *testing.T) {
	ch, stop := signalWinch()
	syscall.Kill(syscall.Getpid(), syscall.SIGWINCH)
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("the winch channel must receive a SIGWINCH before stop")
	}
	stop()
	stop()
	syscall.Kill(syscall.Getpid(), syscall.SIGWINCH)
	select {
	case <-ch:
		t.Fatal("after stop the signal handler must not deliver")
	case <-time.After(300 * time.Millisecond):
	}
}
