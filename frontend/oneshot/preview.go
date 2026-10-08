package oneshot

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

const argBound = 80

var argKeys = []string{"path", "command", "pattern", "query", "url", "task", "id", "name"}

func boundedStart(e core.ToolStart) core.ToolStart {
	return core.ToolStart{Call: core.ToolCall{ID: e.Call.ID, Name: e.Call.Name, Args: boundedArgs(e.Call.Args)}}
}

func boundedArgs(raw json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return preview("")
	}
	for _, key := range argKeys {
		var value string
		if v, ok := fields[key]; ok && json.Unmarshal(v, &value) == nil {
			return preview(oneLine(value))
		}
	}
	return preview("")
}

func preview(s string) json.RawMessage {
	b, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return b
}

func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= argBound {
		return s
	}
	return string([]rune(s)[:argBound])
}
