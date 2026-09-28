package web

import (
	"github.com/mrsirg97-rgb/rig/v2/core"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

type sessionJSON struct {
	ID      string `json:"id"`
	Cwd     string `json:"cwd"`
	Started string `json:"started"`
	Exit    string `json:"exit"`
	Turns   int    `json:"turns"`
	Tokens  int64  `json:"tokens"`
	Label   string `json:"label"`
}

type messageJSON struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Reasoning string         `json:"reasoning,omitempty"`
	ToolCalls []toolCallJSON `json:"tool_calls,omitempty"`
	ToolID    string         `json:"tool_id,omitempty"`
}

type toolCallJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

type usageJSON struct {
	Seq        int64 `json:"seq"`
	Prompt     int64 `json:"prompt"`
	Completion int64 `json:"completion"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

type transcriptJSON struct {
	ID       string        `json:"id"`
	Cwd      string        `json:"cwd"`
	Total    int           `json:"total"`
	Limit    int           `json:"limit"`
	Offset   int           `json:"offset"`
	HasMore  bool          `json:"has_more"`
	Messages []messageJSON `json:"messages"`
	Usage    []usageJSON   `json:"usage"`
}

type modelJSON struct {
	ID         string   `json:"id"`
	Window     int      `json:"window"`
	MaxTokens  int      `json:"max_tokens"`
	Reserve    int      `json:"reserve"`
	KeepRecent int      `json:"keep_recent"`
	Role       string   `json:"role"`
	Effort     string   `json:"effort"`
	Efforts    []string `json:"efforts"`
}

type pluginJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	File        string `json:"file"`
}

func messageJSONOf(m core.Message) messageJSON {
	out := messageJSON{
		Role:      string(m.Role),
		Content:   m.Content,
		Reasoning: m.Reasoning,
		ToolID:    m.ToolID,
	}
	if len(m.ToolCalls) > 0 {
		out.ToolCalls = make([]toolCallJSON, 0, len(m.ToolCalls))
		for _, c := range m.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, toolCallJSON{ID: c.ID, Name: c.Name, Args: string(c.Args)})
		}
	}
	return out
}

func todoProject(cwd string) todostore.Project {
	return todostore.ProjectOf(cwd)
}
