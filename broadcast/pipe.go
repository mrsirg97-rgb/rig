package broadcast

import (
	"bufio"
	"context"
	"io"
	"sync"
)

type pipeTransport struct {
	id  int64
	rw  io.ReadWriter
	enc Encoder

	mu     sync.Mutex
	closed bool
}

func NewPipeTransport(id int64, rw io.ReadWriter, enc Encoder) Transport {
	return &pipeTransport{id: id, rw: rw, enc: enc}
}

func (t *pipeTransport) Id() int64 {
	return t.id
}

func (t *pipeTransport) Send(ctx context.Context, callback func(ack error), messages ...Message) {
	if err := ctx.Err(); err != nil {
		callback(err)
		return
	}
	var frames []byte
	for _, m := range messages {
		frame, err := t.enc.Encode(m)
		if err != nil {
			callback(err)
			return
		}
		frames = append(append(frames, frame...), '\n')
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		callback(context.Canceled)
		return
	}
	_, err := t.rw.Write(frames)
	callback(err)
}

func (t *pipeTransport) Recv(ctx context.Context, callback func(err error, messages ...Message)) {
	context.AfterFunc(ctx, t.Close)
	go func() {
		lines := bufio.NewScanner(t.rw)
		for lines.Scan() {
			m, err := t.enc.Decode(lines.Bytes())
			if err != nil {
				t.Close()
				callback(err)
				return
			}
			callback(nil, m)
		}
		err := lines.Err()
		if err == nil {
			err = io.EOF
		}
		callback(err)
	}()
}

func (t *pipeTransport) Close() {
	t.mu.Lock()
	closed := t.closed
	t.closed = true
	t.mu.Unlock()
	if closed {
		return
	}
	if c, ok := t.rw.(io.Closer); ok {
		c.Close()
	}
}
