package room

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
)

const pipeLineWall = 65535

var testOrigin = int64(2)

func frameOf(t *testing.T, enc broadcast.Encoder, payload evt.Signal, origin int64) string {
	t.Helper()
	frame, err := enc.Encode(broadcast.NewMessage(origin, true, payload))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(frame)
}

func paddedRow(t *testing.T, enc broadcast.Encoder, target int) TableChange {
	t.Helper()
	empty := TableChange{Row: json.RawMessage(`{"id":"local","command":""}`)}
	base := len(frameOf(t, enc, empty, testOrigin))
	if base > target {
		t.Fatalf("the envelope alone passes the target: %d", base)
	}
	return TableChange{Row: json.RawMessage(`{"id":"local","command":"` + strings.Repeat("a", target-base) + `"}`)}
}

func throughPipe(t *testing.T, enc broadcast.Encoder, payload evt.Signal) (broadcast.Message, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	receiver := broadcast.NewPipeTransport(1, r, enc)
	sender := broadcast.NewPipeTransport(2, w, enc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	delivered := make(chan broadcast.Message, 1)
	terminal := make(chan error, 1)
	receiver.Recv(ctx, func(err error, messages ...broadcast.Message) {
		if err != nil {
			terminal <- err
			return
		}
		if len(messages) > 0 {
			delivered <- messages[0]
		}
	})
	go func() {
		sender.Send(ctx, func(error) {}, broadcast.NewMessage(testOrigin, true, payload))
		w.Close()
	}()
	select {
	case err := <-terminal:
		return nil, err
	case m := <-delivered:
		return m, nil
	}
}

func TestTableChangeFrameFitsThePipeLine(t *testing.T) {
	enc := Codec()
	minimal := len(frameOf(t, enc, TableChange{Row: json.RawMessage(`{"id":"local","command":""}`)}, testOrigin))
	swapMinimal := len(frameOf(t, enc, SwapChild{Model: "local"}, testOrigin))

	fitting := paddedRow(t, enc, pipeLineWall)
	m, err := throughPipe(t, enc, fitting)
	if err != nil {
		t.Fatalf("a frame of %d bytes must survive the pipe's line: %v", pipeLineWall, err)
	}
	change, ok := m.Signal().(TableChange)
	if !ok {
		t.Fatalf("the frame arrives as %T", m.Signal())
	}
	if len(change.Row) != len(fitting.Row) {
		t.Fatalf("the row arrived as %d bytes, left as %d", len(change.Row), len(fitting.Row))
	}

	over := paddedRow(t, enc, pipeLineWall+1)
	if _, err := throughPipe(t, enc, over); err == nil {
		t.Fatalf("a frame of %d bytes must close the transport", pipeLineWall+1)
	} else if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("the transport closed with %v, want the scanner's too-long error", err)
	}
	t.Logf("the table change's frame carries a %d-byte envelope and the child swap's a %d-byte one; the pipe's line budgets a %d-byte row and a %d-byte child; one byte over ends the connection", minimal, swapMinimal, pipeLineWall-minimal, pipeLineWall-swapMinimal)
}
