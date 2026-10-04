package graph

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
)

const lspRequestTimeout = 30 * time.Second

var serversMu sync.Mutex

var servers = map[string][]string{
	"typescript": {"typescript-language-server", "--stdio"},
}

func SetServer(language string, argv ...string) {
	serversMu.Lock()
	defer serversMu.Unlock()
	if len(argv) == 0 {
		delete(servers, language)
		return
	}
	servers[language] = argv
}

func ServerOf(language string) []string {
	serversMu.Lock()
	defer serversMu.Unlock()
	return servers[language]
}

type lspClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	lang    string
	voice   broadcast.Member
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan json.RawMessage
	dead    bool
}

func (q *Queue) lspFor(ctx context.Context, lang string) (*lspClient, error) {
	q.lspMu.Lock()
	defer q.lspMu.Unlock()
	if q.lsp != nil {
		if q.lspLang == lang && q.lsp.alive() {
			return q.lsp, nil
		}
		q.lsp.stop()
		q.lsp = nil
	}
	argv := ServerOf(lang)
	if argv == nil {
		return nil, nil
	}
	c, err := startServer(ctx, lang, argv, q.voice)
	if err != nil {
		return nil, err
	}
	q.lsp = c
	q.lspLang = lang
	return c, nil
}

func (q *Queue) stopLSP() {
	q.lspMu.Lock()
	if q.lsp != nil {
		q.lsp.stop()
		q.lsp = nil
	}
	q.lspMu.Unlock()
}

func startServer(ctx context.Context, lang string, argv []string, voice broadcast.Member) (*lspClient, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("graph: lsp %s: %w", lang, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("graph: lsp %s: %w", lang, err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("graph: lsp %s: %w", lang, err)
	}
	c := &lspClient{
		cmd:     cmd,
		stdin:   stdin,
		lang:    lang,
		voice:   voice,
		pending: map[int64]chan json.RawMessage{},
	}
	c.mu.Lock()
	c.nextID = 1
	c.mu.Unlock()
	go c.readLoop(stdout)
	params := map[string]any{
		"processId":    os.Getpid(),
		"rootUri":      nil,
		"capabilities": map[string]any{},
	}
	if _, err := c.request(ctx, "initialize", params); err != nil {
		c.stop()
		return nil, fmt.Errorf("graph: lsp %s initialize: %w", lang, err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		c.stop()
		return nil, fmt.Errorf("graph: lsp %s initialized: %w", lang, err)
	}
	return c, nil
}

func (c *lspClient) alive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.dead
}

func (c *lspClient) stop() {
	c.mu.Lock()
	if c.dead {
		c.mu.Unlock()
		return
	}
	c.dead = true
	cmd := c.cmd
	stdin := c.stdin
	pending := c.pending
	c.pending = map[int64]chan json.RawMessage{}
	c.mu.Unlock()
	_ = stdin.Close()
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	for _, ch := range pending {
		close(ch)
	}
}

func (c *lspClient) say(format string, args ...any) {
	if c.voice != nil {
		broadcast.Say(c.voice, "graph", fmt.Sprintf(format, args...))
	}
}

func (c *lspClient) readLoop(stdout io.Reader) {
	r := bufio.NewReader(stdout)
	for {
		n, err := readHeaders(r)
		if err != nil {
			c.die(err)
			return
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			c.die(err)
			return
		}
		var msg struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		if msg.ID == nil {
			continue
		}
		if msg.Method != "" {
			_ = c.notifyReply(*msg.ID)
			continue
		}
		c.mu.Lock()
		ch := c.pending[*msg.ID]
		delete(c.pending, *msg.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- msg.Result
			close(ch)
		}
	}
}

func readHeaders(r *bufio.Reader) (int, error) {
	n := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return 0, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if n >= 0 {
				return n, nil
			}
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &n); err != nil {
				return 0, err
			}
		}
	}
}

func (c *lspClient) die(err error) {
	c.mu.Lock()
	if c.dead {
		c.mu.Unlock()
		return
	}
	c.dead = true
	pending := c.pending
	c.pending = map[int64]chan json.RawMessage{}
	c.mu.Unlock()
	c.say("the %s language server stopped: %v", c.lang, err)
	for _, ch := range pending {
		close(ch)
	}
}

func (c *lspClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.dead {
		c.mu.Unlock()
		return nil, fmt.Errorf("graph: the %s language server is gone", c.lang)
	}
	id := c.nextID
	c.nextID++
	ch := make(chan json.RawMessage, 1)
	c.pending[id] = ch
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	if err := c.writeLocked(req); err != nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("graph: lsp %s: %w", c.lang, err)
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-time.After(lspRequestTimeout):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("graph: lsp %s: %s timed out", c.lang, method)
	case res, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("graph: the %s language server is gone", c.lang)
		}
		return res, nil
	}
}

func (c *lspClient) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead {
		return fmt.Errorf("graph: the %s language server is gone", c.lang)
	}
	req := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		req["params"] = params
	}
	if err := c.writeLocked(req); err != nil {
		return fmt.Errorf("graph: lsp %s: %w", c.lang, err)
	}
	return nil
}

func (c *lspClient) notifyReply(id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead {
		return fmt.Errorf("graph: the %s language server is gone", c.lang)
	}
	return c.writeLocked(map[string]any{"jsonrpc": "2.0", "id": id, "result": nil})
}

func (c *lspClient) writeLocked(req map[string]any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.stdin.Write(body)
	return err
}

func lspURI(abs string) string {
	return "file://" + abs
}

func lspPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	p, err := url.PathUnescape(u.Path)
	if err != nil {
		return "", err
	}
	return path.Clean(p), nil
}
