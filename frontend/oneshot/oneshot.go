package oneshot

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

type OneShot struct {
	Prompt  string
	Out     io.Writer
	faulted bool
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

func (o *OneShot) Notify(ev core.Event) {
	if o.Out == nil {
		return
	}
	switch e := ev.(type) {
	case core.TextDelta:
		io.WriteString(o.Out, e.Text)
	case core.Done:
		io.WriteString(o.Out, "\n")
	case core.Fault:
		o.faulted = true
		io.WriteString(o.Out, "\nrig: fault: "+e.Err.Error()+"\n")
	}
}

var ErrOneShot = errors.New("oneshot: empty prompt")

func ErrPrompt(prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return ErrOneShot
	}
	return nil
}
