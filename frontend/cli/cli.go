// Package cli is the stdin/stdout Frontend. Input is a blocking pull of one
// user message; Notify is a fire-and-forget observation of the turn stream,
// rendered as greppable plain text: deltas verbatim, calls and faults as
// marked lines. Sequential delivery means no locking.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mrsirg97-rgb/looper/core"
)

type cli struct {
	in  *bufio.Reader
	out io.Writer
}

// New wires the frontend over an input reader and output writer.
func New(in io.Reader, out io.Writer) core.Frontend {
	return &cli{in: bufio.NewReader(in), out: out}
}

// Input blocks for one user message. Blank lines are no-ops; EOF ends the
// REPL; a cancelled context surfaces its error.
func (c *cli) Input(ctx context.Context) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		line, err := c.in.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				if msg := strings.TrimRight(line, "\r\n"); msg != "" {
					return msg, nil // final line without a newline
				}
				return "", io.EOF
			}
			return "", fmt.Errorf("cli: read input: %w", err)
		}
		if strings.TrimSpace(line) == "" {
			continue // blank line: no-op
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
}

// Notify observes one stream event and renders it to the output.
func (c *cli) Notify(ev core.Event) {
	switch e := ev.(type) {
	case core.TextDelta:
		io.WriteString(c.out, e.Text)
	case core.ToolCallEvent:
		fmt.Fprintf(c.out, "\n[call] %s\n", e.Call.Name)
	case core.Done:
		io.WriteString(c.out, "\n")
	case core.Fault:
		fmt.Fprintf(c.out, "\n[fault] %v\n", e.Err)
	}
}
