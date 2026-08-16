package oneshot

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/mrsirg97-rgb/looper/core"
)

// OneShot is the single-prompt Frontend: the first Input yields the prompt,
// the next ends the session (io.EOF is the loop's clean exit). Notify
// renders the turn's assistant text and faults to Out. The scheduler's
// worker path uses it: argv supplies the prompt, the process stdout is
// the response.
type OneShot struct {
	Prompt  string
	Out     io.Writer
	faulted bool // a fault crossed the turn: the process must exit non-zero
}

// Faulted reports whether any fault crossed the session: the run-job
// record derives status from exit, so a faulted worker must exit non-zero
// or the run logs as ok.
func (o *OneShot) Faulted() bool { return o.faulted }

// Input implements Frontend.
func (o *OneShot) Input(ctx context.Context) (string, error) {
	if o.Prompt == "" {
		return "", io.EOF
	}
	p := o.Prompt
	o.Prompt = ""
	return p, nil
}

// Notify implements Frontend: assistant text straight through, faults
// loud. Tool events stay out of the worker's stdout (their results are
// the turn's substance, not its report).
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
		io.WriteString(o.Out, "\nlooper: fault: "+e.Err.Error()+"\n")
	}
}

// ErrOneShot reports an empty-prompt construction: a one-shot with no
// prompt is a construction error, not an empty turn.
var ErrOneShot = errors.New("oneshot: empty prompt")

// ErrPrompt checks the prompt before construction (the loop's empty-turn
// skip would otherwise swallow it as an innocuous blank line).
func ErrPrompt(prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return ErrOneShot
	}
	return nil
}
