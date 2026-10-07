package oneshot

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

// argBound is the band's budget for one argument: the row carries the worker's
// number, the tool's name, this argument and its age, and 80 characters is what
// SPEC_TUI 3a bounds it at. A body is not truncated into the bound, it is not
// in the list; the bound is what a long path or command may cost.
const argBound = 80

// argKeys are the arguments that name a call's work, in the order a reader
// would want them: the file, the command, the pattern, then the rest. `write`'s
// content and `edit`'s old/new are not here, which is the whole point.
var argKeys = []string{"path", "command", "pattern", "query", "url", "task", "id", "name"}

// boundedStart keeps a worker's published tool call to its name and one short
// argument, so the band can show what a worker is doing without ever carrying a
// body across the pipe. The argument rides ToolCall.Args as a JSON string, and
// a call whose work nothing here names crosses as the empty string: the tool's
// name alone, never a guess at the body.
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
