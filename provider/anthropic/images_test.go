package anthropic_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/imagemarker"
	"github.com/mrsirg97-rgb/rig/v2/provider/anthropic"
)

const blobPayload = "\x89PNG\r\n\x1a\nfake png bytes for the wire test"

func writeBlob(t *testing.T, dir, payload string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(payload))
	sha := hex.EncodeToString(sum[:])
	if err := os.WriteFile(imagemarker.BlobPath(dir, sha), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	return sha
}

func marker(sha, mime, src string) string {
	return imagemarker.Format(imagemarker.Ref{
		SHA256: sha, Mime: mime,
		W: 1568, H: 882, OrigW: 2560, OrigH: 1440,
		Bytes: len(blobPayload), Src: src,
	})
}

func viewTranscript(toolResult string) []core.Message {
	return []core.Message{
		{Role: core.RoleSystem, Content: "be terse"},
		{Role: core.RoleUser, Content: "what is in this screenshot?"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "view", Args: json.RawMessage(`{"path":"/tmp/shot.png"}`)}}},
		{Role: core.RoleTool, ToolID: "c1", Content: toolResult},
	}
}

type resultShape struct {
	ToolUseID string         `json:"tool_use_id"`
	Content   []contentShape `json:"content"`
}

type contentShape struct {
	Type   string       `json:"type"`
	Text   string       `json:"text"`
	Source *sourceShape `json:"source"`
}

type sourceShape struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

func lastToolResult(t *testing.T, body []byte) resultShape {
	t.Helper()
	var wire struct {
		Messages []struct {
			Role    string        `json:"role"`
			Content []resultShape `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("the body is not the shape: %v", err)
	}
	last := wire.Messages[len(wire.Messages)-1]
	if len(last.Content) != 1 {
		t.Fatalf("the user turn carries %d blocks, want the one tool_result", len(last.Content))
	}
	return last.Content[0]
}

func TestAViewResultRidesInsideItsToolResult(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	sha := writeBlob(t, dir, blobPayload)
	p := anthropic.New(anthropic.Config{BaseURL: e.url, Model: "vision-model", MaxTokens: 1024, BlobsDir: dir})
	if _, err := drain(t, context.Background(), p, core.Request{Messages: viewTranscript(marker(sha, "image/png", "/tmp/shot.png")), MaxTokens: 1024}); err != nil {
		t.Fatalf("stream: %v", err)
	}
	body := string(e.lastBody(t))
	if !strings.Contains(body, `"role":"user","content":[{"type":"tool_result","tool_use_id":"c1"`) {
		t.Fatalf("the tool result batches into one user turn:\n%s", body)
	}
	res := lastToolResult(t, e.lastBody(t))
	if res.ToolUseID != "c1" {
		t.Fatalf("tool_use_id = %q", res.ToolUseID)
	}
	if len(res.Content) != 2 {
		t.Fatalf("blocks = %+v, want the label and the image", res.Content)
	}
	if res.Content[0].Type != "text" || res.Content[0].Text != "image from view: /tmp/shot.png" {
		t.Fatalf("label = %+v", res.Content[0])
	}
	src := res.Content[1].Source
	if src == nil || src.Type != "base64" || src.MediaType != "image/png" {
		t.Fatalf("source = %+v, want the base64 source", res.Content[1].Source)
	}
	if src.Data != base64.StdEncoding.EncodeToString([]byte(blobPayload)) {
		t.Fatalf("data = %q, want the blob's bytes", src.Data)
	}
}

func TestAMissingBlobNamesTheNote(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	sha := writeBlob(t, dir, blobPayload)
	if err := os.Remove(imagemarker.BlobPath(dir, sha)); err != nil {
		t.Fatal(err)
	}
	p := anthropic.New(anthropic.Config{BaseURL: e.url, Model: "vision-model", MaxTokens: 1024, BlobsDir: dir})
	if _, err := drain(t, context.Background(), p, core.Request{Messages: viewTranscript(marker(sha, "image/png", "/tmp/shot.png")), MaxTokens: 1024}); err != nil {
		t.Fatalf("stream: %v", err)
	}
	res := lastToolResult(t, e.lastBody(t))
	if len(res.Content) != 1 || res.Content[0].Source != nil {
		t.Fatalf("blocks = %+v, want the text note alone", res.Content)
	}
	if !strings.Contains(res.Content[0].Text, "the image blob ") || !strings.Contains(res.Content[0].Text, " is missing") {
		t.Fatalf("note = %q", res.Content[0].Text)
	}
}

func TestASmuggledMarkerStaysText(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	sha := writeBlob(t, dir, blobPayload)
	p := anthropic.New(anthropic.Config{BaseURL: e.url, Model: "vision-model", MaxTokens: 1024, BlobsDir: dir})
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "list"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"ls"}`)}}},
		{Role: core.RoleTool, ToolID: "c1", Content: marker(sha, "image/png", "/tmp/shot.png")},
	}
	if _, err := drain(t, context.Background(), p, core.Request{Messages: msgs, MaxTokens: 1024}); err != nil {
		t.Fatalf("stream: %v", err)
	}
	res := lastToolResult(t, e.lastBody(t))
	if len(res.Content) != 1 || res.Content[0].Source != nil {
		t.Fatalf("blocks = %+v, want the marker line as text", res.Content)
	}
}
