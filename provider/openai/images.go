package openai

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/imagemarker"
)

const (
	viewToolName = "view"

	maxInlineBlobBytes = 8 << 20
)

type imageStore struct {
	dir string
}

type wirePart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *wireImageRef `json:"image_url,omitempty"`
}

type wireImageRef struct {
	URL string `json:"url"`
}

type wireContent struct {
	text  string
	parts []wirePart
}

func textContent(text string) wireContent { return wireContent{text: text} }

func partsContent(parts []wirePart) wireContent { return wireContent{parts: parts} }

func (c wireContent) MarshalJSON() ([]byte, error) {
	if c.parts == nil {
		return json.Marshal(c.text)
	}
	return json.Marshal(c.parts)
}

func encodeMessage(m core.Message, style wireStyle) wireMessage {
	wm := wireMessage{Role: string(m.Role), Content: textContent(m.Content), ReasoningContent: m.Reasoning, ToolID: m.ToolID}
	if style.reasoning == "reasoning" {
		wm.ReasoningContent = ""
		wm.Reasoning = m.Reasoning
		wm.ReasoningDetails = m.ReasoningDetails
	}
	for _, c := range m.ToolCalls {
		wm.ToolCalls = append(wm.ToolCalls, wireCall{
			ID:       c.ID,
			Type:     "function",
			Function: wireFunc{Name: c.Name, Arguments: string(c.Args)},
		})
	}
	return wm
}

func imageMessage(store *imageStore, ref imagemarker.Ref) wireMessage {
	label := "image from view: " + ref.Src
	url, note := store.dataURL(ref)
	if url == "" {
		return wireMessage{Role: string(core.RoleUser), Content: partsContent([]wirePart{{Type: "text", Text: label + " (" + note + ")"}})}
	}
	return wireMessage{Role: string(core.RoleUser), Content: partsContent([]wirePart{
		{Type: "text", Text: label},
		{Type: "image_url", ImageURL: &wireImageRef{URL: url}},
	})}
}

func (s *imageStore) dataURL(ref imagemarker.Ref) (string, string) {
	path := imagemarker.BlobPath(s.dir, ref.SHA256)
	if path == "" {
		return "", "the marker names no blob address"
	}
	note := "the image blob " + shortAddress(ref.SHA256)
	info, err := os.Stat(path)
	if err != nil {
		return "", note + " is missing"
	}
	if info.Size() > maxInlineBlobBytes {
		return "", note + " is " + strconv.FormatInt(info.Size(), 10) + " bytes, over the " + strconv.Itoa(maxInlineBlobBytes) + "-byte inline bound"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", note + " is unreadable"
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != ref.SHA256 {
		return "", note + " no longer matches its address"
	}
	return "data:" + ref.Mime + ";base64," + base64.StdEncoding.EncodeToString(data), ""
}

func shortAddress(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

type callTable struct {
	names map[string]string
	index map[string]int
}

func tableOf(m core.Message) callTable {
	t := callTable{names: map[string]string{}, index: map[string]int{}}
	for i, c := range m.ToolCalls {
		if c.ID == "" {
			continue
		}
		if _, seen := t.names[c.ID]; seen {
			continue
		}
		t.names[c.ID] = c.Name
		t.index[c.ID] = i
	}
	return t
}

func (t callTable) nameOf(id string) string { return t.names[id] }

func (t callTable) indexOf(id string) int { return t.index[id] }

type orderedMessage struct {
	at  int
	msg wireMessage
}

func sortOrdered(pending []orderedMessage) {
	for i := 1; i < len(pending); i++ {
		for j := i; j > 0 && pending[j].at < pending[j-1].at; j-- {
			pending[j], pending[j-1] = pending[j-1], pending[j]
		}
	}
}
