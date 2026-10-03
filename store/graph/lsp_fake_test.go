package graph_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

func TestFakeLSPServer(t *testing.T) {
	if os.Getenv("RIG_FAKE_LSP") != "1" {
		return
	}
	logf := os.Getenv("RIG_FAKE_LSP_LOG")
	note := func(s string) {
		f, err := os.OpenFile(logf, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintf(f, "%s\n", s)
	}
	reply := func(id int64, result any) {
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		if err != nil {
			return
		}
		fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n", len(body))
		_, _ = os.Stdout.Write(body)
	}
	r := bufio.NewReader(os.Stdin)
	for {
		n, err := fakeFrame(r)
		if err != nil {
			note("eof")
			return
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			note("eof")
			return
		}
		var msg struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		note(msg.Method)
		if msg.ID == nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			reply(*msg.ID, map[string]any{"capabilities": map[string]any{}})
		case "textDocument/documentSymbol":
			if b, err := os.ReadFile(os.Getenv("RIG_FAKE_LSP_SYMBOLS")); err == nil {
				reply(*msg.ID, json.RawMessage(b))
			} else {
				reply(*msg.ID, []any{})
			}
		case "textDocument/references":
			if b, err := os.ReadFile(os.Getenv("RIG_FAKE_LSP_REFS")); err == nil {
				reply(*msg.ID, json.RawMessage(b))
			} else {
				reply(*msg.ID, []any{})
			}
		default:
			reply(*msg.ID, nil)
		}
	}
}

func fakeFrame(r *bufio.Reader) (int, error) {
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
