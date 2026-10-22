package python

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"syscall"
	"time"
)

func (k *kernel) waitLoop(p *proc) {
	waitErr := p.cmd.Wait()
	<-p.readDone
	k.onDeath(p, waitErr)
	close(p.dead)
}

func (k *kernel) onDeath(p *proc, _ error) {
	desc := exitDescription(p.cmd.ProcessState)
	tail := p.stderrTail()

	k.mu.Lock()
	current := k.proc == p
	if current {
		k.proc = nil
		k.lastDeath = &deathNote{desc: desc, stderr: tail}
	}
	k.mu.Unlock()
	if !current {
		return
	}
	msg := "kernel exited (" + desc + ")"
	if tail != "" {
		msg += "\n[stderr]\n" + tail
	}
	p.failAll(msg)
}

func (p *proc) failAll(msg string) {
	p.mu.Lock()
	calls := make([]*pendingCall, 0, len(p.pending))
	for id, call := range p.pending {
		calls = append(calls, call)
		delete(p.pending, id)
	}
	p.mu.Unlock()
	for _, call := range calls {
		select {
		case call.ch <- Reply{ID: nil, Ok: false, Error: strPtr(msg)}:
		default:
		}
	}
}

func (k *kernel) send(ctx context.Context, req request, timeoutMs int, onStream func(string)) (Reply, error) {
	select {
	case k.queue <- struct{}{}:
	case <-ctx.Done():
		return Reply{}, fmt.Errorf("python: the caller was cancelled before its cell reached the kernel: %w", ctx.Err())
	}
	defer func() { <-k.queue }()
	if err := ctx.Err(); err != nil {
		return Reply{}, fmt.Errorf("python: the caller was cancelled while the cell queued: %w", err)
	}

	var p *proc
	k.mu.Lock()
	p = k.proc
	k.mu.Unlock()

	var note *string
	if p == nil {
		note = k.takeDeathNote()
		if !k.noBootstrap {
			if err := ensureKernel(ctx); err != nil {
				msg := err.Error()
				return Reply{ID: nil, Ok: false, Error: &msg, Note: note}, nil
			}
		}
		var err error
		k.mu.Lock()
		p, err = k.start()
		k.mu.Unlock()
		if err != nil {
			msg := "kernel failed to start: " + err.Error()
			return Reply{ID: nil, Ok: false, Error: &msg, Note: note}, nil
		}
	}

	id := strconv.FormatInt(k.seq.Add(1), 10)
	req.ID = id
	call := &pendingCall{ch: make(chan Reply, 1), sink: onStream}
	p.mu.Lock()
	p.pending[id] = call
	p.mu.Unlock()

	body, err := json.Marshal(req)
	if err != nil {
		p.forget(id)
		msg := "kernel is not writable: " + err.Error()
		return Reply{ID: strPtr(id), Ok: false, Error: &msg, Note: note}, nil
	}
	body = append(body, '\n')
	if _, err := p.stdin.Write(body); err != nil {

		p.forget(id)
		msg := "kernel is not writable: " + err.Error()
		return Reply{ID: strPtr(id), Ok: false, Error: &msg, Note: note}, nil
	}

	var timerC <-chan time.Time
	if timeoutMs != NoCellCap {
		timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
		defer timer.Stop()
		timerC = timer.C
	}
	select {
	case r := <-call.ch:
		if note != nil {
			r.Note = note
		}
		return r, nil
	case <-timerC:
		msg := fmt.Sprintf("timed out after %ds; kernel will be restarted on the next call; all variables are gone. Re-run setup, or pass a larger timeoutMs.",
			roundSeconds(timeoutMs))
		k.restart()
		return Reply{ID: strPtr(id), Ok: false, Error: &msg, Note: note}, nil
	case <-ctx.Done():
		select {
		case r := <-call.ch:
			if note != nil {
				r.Note = note
			}
			return r, nil
		default:
		}
		p.forget(id)
		k.restart()
		return Reply{}, ctx.Err()
	}
}

func (p *proc) forget(id string) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

func (k *kernel) takeDeathNote() *string {
	k.mu.Lock()
	d := k.lastDeath
	k.lastDeath = nil
	k.mu.Unlock()
	if d == nil {
		return nil
	}
	s := fmt.Sprintf("note: fresh kernel; previous kernel exited (%s); all previous variables are gone", d.desc)
	if d.stderr != "" {
		s += "\n[stderr]\n" + d.stderr
	}
	return strPtr(s)
}

func (k *kernel) restart() { k.teardown("kernel was restarted; all variables are gone") }
func (k *kernel) shutdown() *proc {
	return k.teardown("kernel shut down")
}

func (k *kernel) teardown(msg string) *proc {
	k.mu.Lock()
	p := k.proc
	k.proc = nil
	k.mu.Unlock()
	if p == nil {
		return nil
	}
	p.failAll(msg)
	if p.cmd.Process != nil {
		syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
	return p
}

func roundSeconds(ms int) int {
	return (ms + 500) / 1000
}
