package openai

import "encoding/json"

type wireProvider struct {
	Order []string `json:"order,omitempty"`
}

type wireCacheControl struct {
	Type string `json:"type"`
}

type wireRequest struct {
	Model              string                  `json:"model"`
	Messages           []wireMessage           `json:"messages"`
	Tools              []wireTool              `json:"tools,omitempty"`
	MaxTokens          int                     `json:"max_tokens,omitempty"`
	ReasoningEffort    string                  `json:"reasoning_effort,omitempty"`
	ChatTemplateKwargs *wireChatTemplateKwargs `json:"chat_template_kwargs,omitempty"`
	Provider           *wireProvider           `json:"provider,omitempty"`
	CacheControl       *wireCacheControl       `json:"cache_control,omitempty"`
	Stream             bool                    `json:"stream"`
	StreamOptions      *wireStreamOptions      `json:"stream_options,omitempty"`
}

type wireChatTemplateKwargs struct {
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type wireStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireToolFn struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type wireMessage struct {
	Role             string          `json:"role"`
	Content          wireContent     `json:"content"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Reasoning        string          `json:"reasoning,omitempty"`
	ReasoningDetails json.RawMessage `json:"reasoning_details,omitempty"`
	ToolCalls        []wireCall      `json:"tool_calls,omitempty"`
	ToolID           string          `json:"tool_call_id,omitempty"`
}

type wireCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function wireFunc `json:"function"`
}

type wireFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireTool struct {
	Type     string     `json:"type"`
	Function wireToolFn `json:"function"`
}

type streamChunk struct {
	Model   string       `json:"model"`
	Choices []wireChoice `json:"choices"`
	Usage   *wireUsage   `json:"usage"`
}

type wireChoice struct {
	Delta        wireDelta `json:"delta"`
	FinishReason *string   `json:"finish_reason"`
}

type wireDelta struct {
	Content          string          `json:"content"`
	ReasoningContent string          `json:"reasoning_content"`
	Reasoning        string          `json:"reasoning"`
	ReasoningDetails json.RawMessage `json:"reasoning_details"`
	ToolCalls        []wireDeltaCall `json:"tool_calls"`
}

type wireDeltaCall struct {
	Index    int      `json:"index"`
	ID       string   `json:"id"`
	Function wireFunc `json:"function"`
}

type wireUsage struct {
	PromptTokens        int                     `json:"prompt_tokens"`
	CompletionTokens    int                     `json:"completion_tokens"`
	PromptTokensDetails wirePromptTokensDetails `json:"prompt_tokens_details"`
	Cost                float64                 `json:"cost"`
}

type wirePromptTokensDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}
