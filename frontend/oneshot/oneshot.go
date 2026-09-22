package oneshot

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
)

const DefaultHeartbeat = 30 * time.Second

type OneShot struct {
	Prompt    string
	Out       io.Writer
	Err       io.Writer
	Heartbeat time.Duration
	faulted   bool
	mu        sync.Mutex
	hbStop    chan struct{}
	hbDone    chan struct{}
	toolName  string
}

func (o *OneShot) Faulted() bool { return o.faulted }

func (o *OneShot) Input(ctx context.Context) (string, error) {
	if o.Prompt == "" {
		return "", io.EOF
	}
	p := o.Prompt
	o.Prompt = ""
	return p, nil
}

func (o *OneShot) heartbeatInterval() time.Duration {
	if o.Heartbeat > 0 {
		return o.Heartbeat
	}
	return DefaultHeartbeat
}

func (o *OneShot) startHeartbeat() {
	stop := make(chan struct{})
	done := make(chan struct{})
	o.mu.Lock()
	o.hbStop = stop
	o.hbDone = done
	o.mu.Unlock()
	go func() {
		defer close(done)
		t := time.NewTicker(o.heartbeatInterval())
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				io.WriteString(o.Err, "rig: heartbeat\n")
			}
		}
	}()
}

func (o *OneShot) stopHeartbeat() {
	o.mu.Lock()
	stop, done := o.hbStop, o.hbDone
	o.hbStop, o.hbDone = nil, nil
	o.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
}

func (o *OneShot) Notify(ev core.Event) {
	switch e := ev.(type) {
	case core.TextDelta:
		if o.Out != nil {
			io.WriteString(o.Out, e.Text)
		}
	case core.ReasoningDelta:
		if o.Err != nil {
			io.WriteString(o.Err, e.Text)
		}
	case core.ToolStart:
		if o.Err != nil {
			o.mu.Lock()
			o.toolName = e.Call.Name
			o.mu.Unlock()
			io.WriteString(o.Err, "\nrig: tool "+e.Call.Name+" start\n")
			o.startHeartbeat()
		}
	case core.ToolResult:
		if o.Err != nil {
			o.stopHeartbeat()
			o.mu.Lock()
			name := o.toolName
			o.toolName = ""
			o.mu.Unlock()
			io.WriteString(o.Err, "rig: tool "+name+" end\n")
		}
	case core.Done:
		if o.Out != nil {
			io.WriteString(o.Out, "\n")
		}
	case core.Fault:
		o.faulted = true
		w := o.Err
		if w == nil {
			w = o.Out
		}
		if w != nil {
			io.WriteString(w, "\nrig: fault: "+e.Err.Error()+"\n")
		}
	}
}

var ErrOneShot = errors.New("oneshot: empty prompt")

func ErrPrompt(prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return ErrOneShot
	}
	return nil
}
