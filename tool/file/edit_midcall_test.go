//go:build unix

package file_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/tool/file"
)

func TestEditMidCallDiskChangeRefusesOnTheDigest(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	session := core.NewSession()
	ctx := core.WithSession(context.Background(), session)

	done := make(chan string, 1)
	go func() {
		_, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
			"path":  fifo,
			"edits": hunks([2]string{"alpha", "one"}),
		}))
		if err != nil {
			done <- "refused: " + err.Error()
			return
		}
		done <- "applied"
	}()

	first := make(chan struct{})
	go func() {
		f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			close(first)
			return
		}
		if _, err := f.Write([]byte("alpha\n")); err != nil {
			f.Close()
			close(first)
			return
		}
		f.Close()
		close(first)
	}()
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("the call never read the file")
	}

	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
			if err == nil {
				f.Close()
				time.Sleep(200 * time.Microsecond)
				continue
			}
			if !errors.Is(err, syscall.ENXIO) {
				return
			}
			break
		}
		f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		f.Write([]byte("beta\n"))
		f.Close()
	}()

	select {
	case msg := <-done:
		if !strings.Contains(msg, "changed on disk mid-call") || !strings.Contains(msg, "nothing landed") {
			t.Fatalf("a change on disk between the validation and the write must refuse on the digest, got: %s", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call neither returned nor refused: a change on disk mid-call must refuse on the digest")
	}
}
