package operator

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/tool"
)

type menu struct {
	core.Tool
	verbs []string
}

func Menu(t core.Tool) core.Tool {
	if t == nil {
		return nil
	}
	verbs := tool.Operator(t.Name())
	if len(verbs) == 0 {
		return t
	}
	return &menu{Tool: t, verbs: verbs}
}

func (m *menu) Description() string {
	return dropVerbs(m.Tool.Description(), m.verbs)
}

func (m *menu) Schema() json.RawMessage {
	return schemaWithoutVerbs(m.Tool.Schema(), m.verbs)
}

func schemaWithoutVerbs(raw json.RawMessage, verbs []string) json.RawMessage {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	out := walkSchema(v, verbs)
	if out == nil {
		return raw
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return raw
	}
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n"))
}

func walkSchema(v any, verbs []string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			switch k {
			case "action":
				out[k] = actionWithoutVerbs(val, verbs)
			case "enum":
				out[k] = enumWithoutVerbs(val, verbs)
			default:
				out[k] = walkSchema(val, verbs)
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, val := range x {
			out = append(out, walkSchema(val, verbs))
		}
		return out
	case string:
		return dropVerbs(x, verbs)
	default:
		return v
	}
}

func actionWithoutVerbs(val any, verbs []string) any {
	m, ok := val.(map[string]any)
	if !ok {
		return val
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if k == "enum" {
			out[k] = enumWithoutVerbs(v, verbs)
			continue
		}
		out[k] = walkSchema(v, verbs)
	}
	return out
}

func enumWithoutVerbs(val any, verbs []string) any {
	items, ok := val.([]any)
	if !ok {
		return val
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && contains(verbs, s) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func contains(verbs []string, v string) bool {
	for _, verb := range verbs {
		if verb == v {
			return true
		}
	}
	return false
}

func dropVerbs(text string, verbs []string) string {
	out := dropVerbClauses(text, verbs)
	return dropVerbTokens(out, verbs)
}

func dropVerbClauses(text string, verbs []string) string {
	parts := strings.Split(text, ";")
	kept := make([]string, 0, len(parts))
	dropped := false
	for _, p := range parts {
		if leadsWithVerb(p, verbs) {
			dropped = true
			continue
		}
		kept = append(kept, p)
	}
	if !dropped {
		return text
	}
	return strings.Join(kept, ";")
}

func leadsWithVerb(clause string, verbs []string) bool {
	w := strings.TrimLeft(clause, " \n\t")
	end := 0
	for end < len(w) && isLetter(w[end]) {
		end++
	}
	if end == 0 {
		return false
	}
	return contains(verbs, w[:end])
}

func dropVerbTokens(text string, verbs []string) string {
	for _, verb := range verbs {
		text = dropToken(text, verb)
	}
	return text
}

func dropToken(text, verb string) string {
	var b bytes.Buffer
	for {
		i := indexVerb(text, verb)
		if i < 0 {
			b.WriteString(text)
			return b.String()
		}
		start, end := i, i+len(verb)
		if end < len(text) && isSeparator(text[end]) {
			for end < len(text) && isSeparator(text[end]) {
				end++
			}
		} else {
			for start > 0 && isSeparator(text[start-1]) {
				start--
			}
		}
		b.WriteString(text[:start])
		text = text[end:]
	}
}

func indexVerb(text, verb string) int {
	for i := 0; i+len(verb) <= len(text); i++ {
		if text[i:i+len(verb)] != verb {
			continue
		}
		if i > 0 && isLetter(text[i-1]) {
			continue
		}
		if i+len(verb) < len(text) && isLetter(text[i+len(verb)]) {
			continue
		}
		return i
	}
	return -1
}

func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isSeparator(c byte) bool {
	return c == ' ' || c == ',' || c == ':' || c == '/' || c == ';'
}
