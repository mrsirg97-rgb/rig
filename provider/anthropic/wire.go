package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/imagemarker"
)

const (
	roleUser      = "user"
	roleAssistant = "assistant"

	cacheEphemeral = "ephemeral"
)

type wireCacheControl struct {
	Type string `json:"type"`
}

type wireRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    []wireText    `json:"system,omitempty"`
	Tools     []wireTool    `json:"tools,omitempty"`
	Thinking  *wireThinking `json:"thinking,omitempty"`
	Messages  []wireMessage `json:"messages"`
	Stream    bool          `json:"stream"`
}

type wireThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type wireText struct {
	Type         string            `json:"type"`
	Text         string            `json:"text"`
	CacheControl *wireCacheControl `json:"cache_control,omitempty"`
}

type wireTool struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	InputSchema  json.RawMessage   `json:"input_schema"`
	CacheControl *wireCacheControl `json:"cache_control,omitempty"`
}

type wireMessage struct {
	Role    string        `json:"role"`
	Content []wireContent `json:"content"`
}

type wireSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type wireContent struct {
	Type         string            `json:"type"`
	Text         string            `json:"text,omitempty"`
	Thinking     string            `json:"thinking,omitempty"`
	Signature    string            `json:"signature,omitempty"`
	Data         string            `json:"data,omitempty"`
	ID           string            `json:"id,omitempty"`
	Name         string            `json:"name,omitempty"`
	Input        json.RawMessage   `json:"input,omitempty"`
	ToolUseID    string            `json:"tool_use_id,omitempty"`
	Content      []wireContent     `json:"content,omitempty"`
	Source       *wireSource       `json:"source,omitempty"`
	CacheControl *wireCacheControl `json:"cache_control,omitempty"`
}

type thinkingRecord struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
}

type wireEvent struct {
	Type    string            `json:"type"`
	Index   int               `json:"index"`
	Message *wireMessageStart `json:"message,omitempty"`
	Block   *wireBlock        `json:"content_block,omitempty"`
	Delta   *wireDelta        `json:"delta,omitempty"`
	Usage   *wireUsageDelta   `json:"usage,omitempty"`
	Error   *wireError        `json:"error,omitempty"`
}

type wireMessageStart struct {
	Model string    `json:"model"`
	Usage wireUsage `json:"usage"`
}

type wireUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

type wireUsageDelta struct {
	OutputTokens int `json:"output_tokens"`
}

type wireBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Data string `json:"data,omitempty"`
}

type wireDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

type wireError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type wireErrorEnvelope struct {
	Error wireError `json:"error"`
}

func (p *provider) encode(req core.Request) ([]byte, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.maxTokens
	}
	if maxTokens == 0 {
		return nil, fmt.Errorf("anthropic: request carries no max_tokens")
	}
	var system []wireText
	msgs := make([]core.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == core.RoleSystem {
			system = append(system, wireText{Type: "text", Text: m.Content})
			continue
		}
		msgs = append(msgs, m)
	}
	if p.cache && len(system) > 0 {
		system[len(system)-1].CacheControl = &wireCacheControl{Type: cacheEphemeral}
	}
	out := wireRequest{
		Model:     p.model,
		MaxTokens: maxTokens,
		System:    system,
		Tools:     p.wireTools(req.Tools),
		Messages:  p.wireMessages(msgs),
		Stream:    true,
	}
	if p.budget > 0 {
		out.Thinking = &wireThinking{Type: "enabled", BudgetTokens: p.budget}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("anthropic: encode request: %w", err)
	}
	return body, nil
}

func (p *provider) wireTools(specs []core.ToolSpec) []wireTool {
	out := make([]wireTool, 0, len(specs))
	for _, s := range specs {
		out = append(out, wireTool{Name: s.Name, Description: s.Description, InputSchema: s.Schema})
	}
	if p.cache && len(out) > 0 {
		out[len(out)-1].CacheControl = &wireCacheControl{Type: cacheEphemeral}
	}
	return out
}

func (p *provider) wireMessages(msgs []core.Message) []wireMessage {
	out := make([]wireMessage, 0, len(msgs))
	var pending []wireContent
	var owner callTable
	flush := func() {
		if len(pending) == 0 {
			return
		}
		out = append(out, wireMessage{Role: roleUser, Content: pending})
		pending = nil
	}
	for _, m := range msgs {
		switch m.Role {
		case core.RoleAssistant:
			flush()
			out = append(out, p.assistantMessage(m))
			owner = tableOf(m)
		case core.RoleTool:
			pending = append(pending, p.toolResultBlock(m, owner))
		default:
			pending = append(pending, wireContent{Type: "text", Text: m.Content})
		}
	}
	flush()
	markTranscriptBreakpoint(out, p.cache)
	return out
}

func markTranscriptBreakpoint(msgs []wireMessage, cache bool) {
	if !cache || len(msgs) < 2 {
		return
	}
	if msgs[len(msgs)-1].Role != roleUser {
		return
	}
	for i := len(msgs) - 2; i >= 0; i-- {
		if msgs[i].Role == roleUser && len(msgs[i].Content) > 0 {
			msgs[i].Content[len(msgs[i].Content)-1].CacheControl = &wireCacheControl{Type: cacheEphemeral}
			return
		}
	}
}

func (p *provider) assistantMessage(m core.Message) wireMessage {
	var blocks []wireContent
	if p.budget > 0 {
		blocks = append(blocks, thinkingBlocks(m.ReasoningDetails)...)
	}
	for _, c := range m.ToolCalls {
		blocks = append(blocks, wireContent{Type: "tool_use", ID: c.ID, Name: c.Name, Input: inputOf(c.Args)})
	}
	if m.Content != "" {
		blocks = append(blocks, wireContent{Type: "text", Text: m.Content})
	}
	return wireMessage{Role: roleAssistant, Content: blocks}
}

func inputOf(args json.RawMessage) json.RawMessage {
	if len(args) > 0 && json.Valid(args) {
		return args
	}
	return json.RawMessage("{}")
}

func thinkingBlocks(details json.RawMessage) []wireContent {
	var records []thinkingRecord
	if err := json.Unmarshal(details, &records); err != nil {
		return nil
	}
	out := make([]wireContent, 0, len(records))
	for _, r := range records {
		out = append(out, wireContent{Type: r.Type, Thinking: r.Thinking, Signature: r.Signature, Data: r.Data})
	}
	return out
}

func (p *provider) toolResultBlock(m core.Message, owner callTable) wireContent {
	inner := []wireContent{{Type: "text", Text: m.Content}}
	if p.blobs != nil && owner.nameOf(m.ToolID) == viewToolName {
		if ref, ok := imagemarker.Parse(m.Content); ok {
			inner = p.imageBlocks(ref)
		}
	}
	return wireContent{Type: "tool_result", ToolUseID: m.ToolID, Content: inner}
}

func (p *provider) imageBlocks(ref imagemarker.Ref) []wireContent {
	label := "image from view: " + ref.Src
	b, note := p.blobs.source(ref)
	if note != "" {
		return []wireContent{{Type: "text", Text: label + " (" + note + ")"}}
	}
	return []wireContent{
		{Type: "text", Text: label},
		{Type: "image", Source: &wireSource{Type: "base64", MediaType: b.mime, Data: b.data}},
	}
}

type callTable struct {
	names map[string]string
}

func tableOf(m core.Message) callTable {
	t := callTable{names: map[string]string{}}
	for _, c := range m.ToolCalls {
		if c.ID == "" {
			continue
		}
		if _, seen := t.names[c.ID]; seen {
			continue
		}
		t.names[c.ID] = c.Name
	}
	return t
}

func (t callTable) nameOf(id string) string { return t.names[id] }
