package openai_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/imagemarker"
	"github.com/mrsirg97-rgb/rig/provider/openai"
)

const sseDone = `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}

data: [DONE]

`

type endpoint struct {
	mu     sync.Mutex
	bodies [][]byte
	url    string
}

func captureEndpoint(t *testing.T) *endpoint {
	t.Helper()
	e := &endpoint{}
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	e.url = srv.URL
	return e
}

func (e *endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	e.mu.Lock()
	e.bodies = append(e.bodies, b)
	e.mu.Unlock()
	io.WriteString(w, sseDone)
}

func (e *endpoint) all() [][]byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([][]byte{}, e.bodies...)
}

func (e *endpoint) last(t *testing.T) wireBodyShape {
	t.Helper()
	bodies := e.all()
	if len(bodies) == 0 {
		t.Fatal("the endpoint saw no request")
	}
	return parseBody(t, bodies[len(bodies)-1])
}

type wireBodyShape struct {
	Messages []wireMsgShape `json:"messages"`
}

type wireMsgShape struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	ToolCalls  []struct {
		ID       string `json:"id"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tool_calls"`
}

type wirePartShape struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

func parseBody(t *testing.T, raw []byte) wireBodyShape {
	t.Helper()
	var b wireBodyShape
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("the request body is not the expected shape: %v\n%s", err, raw)
	}
	return b
}

func (m wireMsgShape) isString(t *testing.T) bool {
	t.Helper()
	return len(m.Content) > 0 && m.Content[0] == '"'
}

func (m wireMsgShape) text(t *testing.T) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(m.Content, &s); err != nil {
		t.Fatalf("%s content is not a string: %s", m.Role, m.Content)
	}
	return s
}

func (m wireMsgShape) parts(t *testing.T) []wirePartShape {
	t.Helper()
	var p []wirePartShape
	if err := json.Unmarshal(m.Content, &p); err != nil {
		t.Fatalf("%s content is not a part array: %s", m.Role, m.Content)
	}
	return p
}

func (m wireMsgShape) roleText(t *testing.T) string {
	t.Helper()
	parts := m.parts(t)
	return parts[0].Text
}

func roles(b wireBodyShape) string {
	var out []string
	for _, m := range b.Messages {
		out = append(out, m.Role)
	}
	return strings.Join(out, ",")
}

func (m wireMsgShape) partAt(t *testing.T, i int) wirePartShape {
	t.Helper()
	parts := m.parts(t)
	if i >= len(parts) {
		t.Fatalf("the message has %d parts, wanted index %d", len(parts), i)
	}
	return parts[i]
}

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

func viewTranscript(t *testing.T, dir string) ([]core.Message, string) {
	t.Helper()
	sha := writeBlob(t, dir, blobPayload)
	return []core.Message{
		{Role: core.RoleSystem, Content: "be terse"},
		{Role: core.RoleUser, Content: "what is in this screenshot?"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "view", Args: json.RawMessage(`{"path":"/tmp/shot.png"}`)}}},
		{Role: core.RoleTool, ToolID: "c1", Content: marker(sha, "image/png", "/tmp/shot.png")},
	}, sha
}

func streamMustNotFault(t *testing.T, p core.Provider, msgs []core.Message) {
	t.Helper()
	events, err := drain(t, context.Background(), p, core.Request{Messages: msgs})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for _, ev := range events {
		if f, ok := ev.(core.Fault); ok {
			t.Fatalf("the image path must never fault the request: %v", f.Err)
		}
	}
}

func TestAViewResultBecomesAUserImagePart(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	msgs, _ := viewTranscript(t, dir)
	streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", dir), msgs)

	body := e.last(t)
	if got := roles(body); got != "system,user,assistant,tool,user" {
		t.Fatalf("roles = %s, want the synthetic user message after the tool batch", got)
	}
	last := body.Messages[len(body.Messages)-1]
	if last.ToolCallID != "" {
		t.Fatal("the synthetic message is a user message, not a tool message")
	}
	if p := last.partAt(t, 0); p.Type != "text" || p.Text != "image from view: /tmp/shot.png" {
		t.Fatalf("the text part = %+v, want the source named", p)
	}
	p := last.partAt(t, 1)
	if p.Type != "image_url" || p.ImageURL == nil {
		t.Fatalf("the image part = %+v", p)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(blobPayload))
	if p.ImageURL.URL != want {
		t.Fatalf("the data URL is %q, want the blob's bytes", p.ImageURL.URL)
	}
	if len(last.parts(t)) != 2 {
		t.Fatalf("exactly two parts: %+v", last.parts(t))
	}
}

func TestTheToolMessageKeepsItsStringContentAndText(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	msgs, sha := viewTranscript(t, dir)
	streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", dir), msgs)

	body := e.last(t)
	tool := body.Messages[3]
	if !tool.isString(t) {
		t.Fatalf("the tool message must stay a string content: %s", tool.Content)
	}
	if tool.text(t) != marker(sha, "image/png", "/tmp/shot.png") {
		t.Fatalf("the tool message is sent unchanged: %q", tool.Content)
	}
	if !body.Messages[1].isString(t) {
		t.Fatal("a plain user message keeps the string shape too")
	}
	if !body.Messages[0].isString(t) {
		t.Fatal("the system message keeps the string shape too")
	}
}

func TestTheImagePartFollowsTheLastToolMessageOfTheBatch(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	sha := writeBlob(t, dir, blobPayload)
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "look and list"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
			{ID: "c1", Name: "view", Args: json.RawMessage(`{"path":"/tmp/shot.png"}`)},
			{ID: "c2", Name: "ls", Args: json.RawMessage(`{"path":"/tmp"}`)},
			{ID: "c3", Name: "view", Args: json.RawMessage(`{"path":"/tmp/other.png"}`)},
		}},
		{Role: core.RoleTool, ToolID: "c1", Content: marker(sha, "image/png", "/tmp/shot.png")},
		{Role: core.RoleTool, ToolID: "c2", Content: "shot.png\nother.png"},
		{Role: core.RoleTool, ToolID: "c3", Content: marker(sha, "image/png", "/tmp/other.png")},
		{Role: core.RoleAssistant, Content: "two images"},
	}
	streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", dir), msgs)

	body := e.last(t)
	want := "user,assistant,tool,tool,tool,user,user,assistant"
	if got := roles(body); got != want {
		t.Fatalf("roles = %s, want %s (every tool message of the batch stays contiguous)", got, want)
	}
	if body.Messages[5].roleText(t) != "image from view: /tmp/shot.png" {
		t.Fatal("the views land in tool-call order")
	}
	if body.Messages[6].roleText(t) != "image from view: /tmp/other.png" {
		t.Fatal("the views land in tool-call order")
	}
}

func TestTwoViewsLandInToolCallOrderWhateverTheResultOrder(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	shaA := writeBlob(t, dir, "the first blob")
	shaB := writeBlob(t, dir, "the second blob")
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "both"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
			{ID: "cA", Name: "view", Args: json.RawMessage(`{}`)},
			{ID: "cB", Name: "view", Args: json.RawMessage(`{}`)},
		}},
		{Role: core.RoleTool, ToolID: "cB", Content: marker(shaB, "image/jpeg", "/tmp/b.jpg")},
		{Role: core.RoleTool, ToolID: "cA", Content: marker(shaA, "image/png", "/tmp/a.png")},
	}
	streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", dir), msgs)

	body := e.last(t)
	first, second := body.Messages[4], body.Messages[5]
	if first.roleText(t) != "image from view: /tmp/a.png" || second.roleText(t) != "image from view: /tmp/b.jpg" {
		t.Fatalf("order = %q then %q, want cA then cB", first.roleText(t), second.roleText(t))
	}
	if !strings.HasPrefix(first.partAt(t, 1).ImageURL.URL, "data:image/png;base64,") {
		t.Fatal("each image carries its own mime")
	}
	if !strings.HasPrefix(second.partAt(t, 1).ImageURL.URL, "data:image/jpeg;base64,") {
		t.Fatal("each image carries its own mime")
	}
}

func TestAMarkerFromAnyOtherToolIsTextOnly(t *testing.T) {
	dir := t.TempDir()
	sha := writeBlob(t, dir, blobPayload)
	for _, toolName := range []string{"read", "grep", "web_fetch", "bash", "python", "viewx", "view "} {
		e := captureEndpoint(t)
		msgs := []core.Message{
			{Role: core.RoleUser, Content: "read it"},
			{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: toolName, Args: json.RawMessage(`{}`)}}},
			{Role: core.RoleTool, ToolID: "c1", Content: marker(sha, "image/png", "/tmp/shot.png")},
		}
		streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", dir), msgs)
		if got := roles(e.last(t)); got != "user,assistant,tool" {
			t.Fatalf("%q: roles = %s, want no image message for a marker %s wrote", toolName, got, toolName)
		}
	}
}

func TestAMarkerInAUserOrAssistantMessageIsNotHonored(t *testing.T) {
	dir := t.TempDir()
	sha := writeBlob(t, dir, blobPayload)
	line := marker(sha, "image/png", "/tmp/shot.png")
	for _, m := range []core.Message{
		{Role: core.RoleUser, Content: line},
		{Role: core.RoleAssistant, Content: line},
		{Role: core.RoleSystem, Content: line},
	} {
		e := captureEndpoint(t)
		streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", dir), []core.Message{m})
		body := e.last(t)
		if got := roles(body); got != string(m.Role) {
			t.Fatalf("%s: roles = %s, want the message alone", m.Role, got)
		}
		if !body.Messages[0].isString(t) {
			t.Fatalf("%s: content must stay text: %s", m.Role, body.Messages[0].Content)
		}
	}
}

func TestAToolResultWhoseCallTheTranscriptNeverIssuedIsTextOnly(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	sha := writeBlob(t, dir, blobPayload)
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "hi"},
		{Role: core.RoleTool, ToolID: "dangling", Content: marker(sha, "image/png", "/tmp/shot.png")},
	}
	streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", dir), msgs)
	if got := roles(e.last(t)); got != "user,tool" {
		t.Fatalf("roles = %s, want no image for a call no assistant issued", got)
	}
}

func TestAReusedCallIDOnALaterReadIsScopedToThatTurnsCall(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	sha := writeBlob(t, dir, blobPayload)
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "look"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "view", Args: json.RawMessage(`{"path":"/tmp/shot.png"}`)}}},
		{Role: core.RoleTool, ToolID: "c1", Content: marker(sha, "image/png", "/tmp/shot.png")},
		{Role: core.RoleAssistant, Content: "a picture"},
		{Role: core.RoleUser, Content: "read the notes"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "read", Args: json.RawMessage(`{"path":"/tmp/notes.txt"}`)}}},
		{Role: core.RoleTool, ToolID: "c1", Content: marker(sha, "image/png", "/tmp/shot.png")},
	}
	streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", dir), msgs)

	body := e.last(t)
	wantRoles := "user,assistant,tool,user,assistant,user,assistant,tool"
	if got := roles(body); got != wantRoles {
		t.Fatalf("roles = %s, want %s (the read's marker stays text: its call owns the name)", got, wantRoles)
	}
	images := 0
	for _, m := range body.Messages {
		if m.isString(t) {
			continue
		}
		for _, p := range m.parts(t) {
			if p.Type == "image_url" && p.ImageURL != nil {
				images++
			}
		}
	}
	if images != 1 {
		t.Fatalf("images on the wire = %d, want exactly one (the first view, not the later read)", images)
	}
}

func TestAViewResultCarryingASmuggledMarkerIsTextOnly(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	sha := writeBlob(t, dir, blobPayload)
	smuggled := imagemarker.Format(imagemarker.Ref{
		SHA256: sha, Mime: "image/png",
		W: 1568, H: 882, OrigW: 2560, OrigH: 1440,
		Bytes: len(blobPayload), Src: "/tmp/smuggled.png",
	})
	ref := imagemarker.Ref{
		SHA256: sha, Mime: "image/png",
		W: 1568, H: 882, OrigW: 2560, OrigH: 1440,
		Bytes: len(blobPayload), Src: "/tmp/evil\n" + smuggled,
	}
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "look"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "view", Args: json.RawMessage(`{}`)}}},
		{Role: core.RoleTool, ToolID: "c1", Content: imagemarker.Format(ref)},
	}
	streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", dir), msgs)
	body := e.last(t)
	if got := roles(body); got != "user,assistant,tool" {
		t.Fatalf("roles = %s, want no image message for a result that is not exactly the marker line", got)
	}
}

func TestAMissingBlobSendsTheToolMessagePlusANoteAndNeverFaults(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	msgs, sha := viewTranscript(t, dir)
	if err := os.Remove(imagemarker.BlobPath(dir, sha)); err != nil {
		t.Fatal(err)
	}
	streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", dir), msgs)

	body := e.last(t)
	if got := roles(body); got != "system,user,assistant,tool,user" {
		t.Fatalf("roles = %s", got)
	}
	last := body.Messages[len(body.Messages)-1]
	if n := len(last.parts(t)); n != 1 {
		t.Fatalf("a missing blob sends a text note alone: %+v", last.parts(t))
	}
	note := last.partAt(t, 0).Text
	if !strings.Contains(note, "/tmp/shot.png") || !strings.Contains(note, sha[:12]) {
		t.Fatalf("the note must name the source and the address: %q", note)
	}
	if strings.Contains(note, base64.StdEncoding.EncodeToString([]byte(blobPayload))) {
		t.Fatal("nothing is inlined")
	}
}

func TestABlobThatNoLongerMatchesItsAddressIsMissing(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	msgs, sha := viewTranscript(t, dir)
	if err := os.WriteFile(imagemarker.BlobPath(dir, sha), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", dir), msgs)
	note := e.last(t).Messages[4].partAt(t, 0).Text
	if strings.Contains(note, "tampered") {
		t.Fatalf("a tampered blob is not an image, and its bytes never travel: %q", note)
	}
}

func TestAnOversizedBlobIsNotInlined(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, (8<<20)+1)
	for i := range big {
		big[i] = byte(i)
	}
	sum := sha256.Sum256(big)
	sha := hex.EncodeToString(sum[:])
	if err := os.WriteFile(imagemarker.BlobPath(dir, sha), big, 0o644); err != nil {
		t.Fatal(err)
	}
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "look"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "view"}}},
		{Role: core.RoleTool, ToolID: "c1", Content: marker(sha, "image/png", "/tmp/big.png")},
	}
	streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", dir), msgs)
	body := e.last(t)
	if got := roles(body); got != "user,assistant,tool,user" {
		t.Fatalf("roles = %s", got)
	}
	if n := len(body.Messages[3].parts(t)); n != 1 {
		t.Fatalf("a blob over the inline bound is refused, not sent: %d parts", n)
	}
}

func TestAnUnreadableBlobIsNotAFault(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 64)
	if err := os.MkdirAll(imagemarker.BlobPath(dir, sha), 0o755); err != nil {
		t.Fatal(err)
	}
	e := captureEndpoint(t)
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "look"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "view"}}},
		{Role: core.RoleTool, ToolID: "c1", Content: marker(sha, "image/png", "/tmp/dir-as-blob.png")},
	}
	streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", dir), msgs)
	if n := len(e.last(t).Messages[3].parts(t)); n != 1 {
		t.Fatalf("a directory where the blob belongs degrades to the note: %d parts", n)
	}
}

func TestANonVisionProviderSendsTheToolTextOnly(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	msgs, _ := viewTranscript(t, dir)
	streamMustNotFault(t, openai.New(e.url, "text-model"), msgs)
	body := e.last(t)
	if got := roles(body); got != "system,user,assistant,tool" {
		t.Fatalf("roles = %s, want no image message at all for a non-vision model", got)
	}
	if !body.Messages[3].isString(t) {
		t.Fatal("the marker line rides as the tool's text")
	}
}

func TestAVisionProviderWithNoBlobsDirSendsTheToolTextOnly(t *testing.T) {
	dir := t.TempDir()
	sha := writeBlob(t, dir, blobPayload)
	e := captureEndpoint(t)
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "look"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "view"}}},
		{Role: core.RoleTool, ToolID: "c1", Content: marker(sha, "image/png", "/tmp/shot.png")},
	}
	streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", ""), msgs)
	if got := roles(e.last(t)); got != "user,assistant,tool" {
		t.Fatalf("roles = %s, want the text path when no store is configured", got)
	}
}

func TestTwoAssembliesOfOneTranscriptAreByteIdentical(t *testing.T) {
	dir := t.TempDir()
	msgs, _ := viewTranscript(t, dir)
	msgs = append(msgs,
		core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
			{ID: "c2", Name: "view"}, {ID: "c3", Name: "view"},
		}},
		core.Message{Role: core.RoleTool, ToolID: "c2", Content: marker(writeBlob(t, dir, "second"), "image/png", "/tmp/two/with spaces.png")},
		core.Message{Role: core.RoleTool, ToolID: "c3", Content: "no marker here"},
	)
	e := captureEndpoint(t)
	p := openai.NewWithVision(e.url, "vision-model", dir)
	streamMustNotFault(t, p, msgs)
	streamMustNotFault(t, p, msgs)
	bodies := e.all()
	if len(bodies) != 2 {
		t.Fatalf("bodies = %d, want 2", len(bodies))
	}
	if string(bodies[0]) != string(bodies[1]) {
		t.Fatalf("two assemblies of one transcript must be byte-identical:\n a: %s\n b: %s", bodies[0], bodies[1])
	}
}

func TestTheImagePartIsStableAcrossTurnsOfAGrowingTranscript(t *testing.T) {
	dir := t.TempDir()
	e := captureEndpoint(t)
	msgs, _ := viewTranscript(t, dir)
	p := openai.NewWithVision(e.url, "vision-model", dir)
	streamMustNotFault(t, p, msgs)
	before := e.last(t)
	grown := append(append([]core.Message{}, msgs...),
		core.Message{Role: core.RoleAssistant, Content: "a screenshot of a cat"},
		core.Message{Role: core.RoleUser, Content: "and now?"},
	)
	streamMustNotFault(t, p, grown)
	after := e.last(t)
	if len(after.Messages) <= len(before.Messages) {
		t.Fatalf("the grown transcript must carry more messages: %d vs %d", len(after.Messages), len(before.Messages))
	}
	for i := range before.Messages {
		if string(before.Messages[i].Content) != string(after.Messages[i].Content) {
			t.Fatalf("message %d changed between turns: the cached prefix must be stable", i)
		}
	}
}

func TestTheDataURLIsWellFormedForEveryAcceptedMime(t *testing.T) {
	dir := t.TempDir()
	for _, mime := range []string{"image/png", "image/jpeg"} {
		e := captureEndpoint(t)
		sha := writeBlob(t, dir, blobPayload)
		msgs := []core.Message{
			{Role: core.RoleUser, Content: "look"},
			{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "view"}}},
			{Role: core.RoleTool, ToolID: "c1", Content: marker(sha, mime, "/tmp/x.png")},
		}
		streamMustNotFault(t, openai.NewWithVision(e.url, "vision-model", dir), msgs)
		url := e.last(t).Messages[3].partAt(t, 1).ImageURL.URL
		if !strings.HasPrefix(url, "data:"+mime+";base64,") {
			t.Fatalf("url = %q, want the %s data URL", url, mime)
		}
		if _, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:"+mime+";base64,")); err != nil {
			t.Fatalf("the base64 must decode: %v", err)
		}
	}
}

func TestTheBlobIsReadFromTheMarkersAddress(t *testing.T) {
	dir := t.TempDir()
	sha := writeBlob(t, dir, blobPayload)
	if _, err := os.Stat(filepath.Join(dir, sha)); err != nil {
		t.Fatalf("the fixture blob is not at the address the marker names: %v", err)
	}
}
