package view

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	maxCDPMessage = 32 << 20
	eventBuf      = 256
)

var errPipeClosed = errors.New("the browser closed its pipe")

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

func (e *cdpError) Error() string {
	if e.Data != "" {
		return e.Message + ": " + e.Data
	}
	return e.Message
}

type cdpMessage struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
}

type cdpConn struct {
	in  io.WriteCloser
	out io.Reader

	writeMu sync.Mutex
	mu      sync.Mutex
	seq     int64
	pending map[int64]chan cdpMessage
	events  chan cdpMessage
	dead    chan struct{}
	once    sync.Once
	err     error
}

func newCDP(in io.WriteCloser, out io.Reader) *cdpConn {
	c := &cdpConn{
		in:      in,
		out:     out,
		pending: map[int64]chan cdpMessage{},
		events:  make(chan cdpMessage, eventBuf),
		dead:    make(chan struct{}),
	}
	go c.read()
	return c
}

func (c *cdpConn) read() {
	r := bufio.NewReaderSize(c.out, 64<<10)
	for {
		body, err := readNul(r, maxCDPMessage)
		if err != nil {
			c.fail(err)
			return
		}
		var m cdpMessage
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		if m.ID == 0 {
			select {
			case c.events <- m:
			case <-c.dead:
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[m.ID]
		delete(c.pending, m.ID)
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch <- m:
			default:
			}
		}
	}
}

func (c *cdpConn) fail(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		if c.err == nil {
			c.err = err
		}
		c.mu.Unlock()
		close(c.dead)
	})
}

func (c *cdpConn) call(ctx context.Context, method string, params any, sessionID string) (json.RawMessage, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.seq++
	id := c.seq
	ch := make(chan cdpMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	msg := cdpMessage{ID: id, Method: method, Params: body, SessionID: sessionID}
	line, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	c.writeMu.Lock()
	_, err = c.in.Write(append(line, 0))
	c.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("the browser is not accepting commands: %v", err)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.dead:
		c.mu.Lock()
		deadErr := c.err
		c.mu.Unlock()
		if deadErr != nil {
			return nil, deadErr
		}
		return nil, errPipeClosed
	case m := <-ch:
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	}
}

func (c *cdpConn) close() {
	c.in.Close()
	c.fail(errPipeClosed)
}

func readNul(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice(0)
		if len(buf)+len(chunk) > max {
			return nil, fmt.Errorf("browser message over %d bytes", max)
		}
		buf = append(buf, chunk...)
		if err == nil {
			return buf[:len(buf)-1], nil
		}
		if err != bufio.ErrBufferFull {
			if err == io.EOF {
				return nil, errPipeClosed
			}
			return nil, err
		}
	}
}
