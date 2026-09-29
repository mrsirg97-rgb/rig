package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func JSONShape(v any) string {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+": "+valueShape(t[k]))
		}
		return "object{" + strings.Join(parts, ", ") + "}"
	case []any:
		return fmt.Sprintf("array[%d]", len(t))
	case nil:
		return "null"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func valueShape(v any) string {
	switch t := v.(type) {
	case []any:
		return fmt.Sprintf("array[%d]", len(t))
	case map[string]any:
		return "object"
	case nil:
		return "null"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func isJSONContent(ct string) bool {
	media := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	return media == "application/json" || strings.HasSuffix(media, "+json")
}

func jsonReply(f Fetched) (string, bool) {
	if !isJSONContent(f.ContentType) && !json.Valid([]byte(f.Body)) {
		return "", false
	}
	var v any
	if err := json.Unmarshal([]byte(f.Body), &v); err != nil {
		return "", false
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(f.Body)); err != nil {
		return "", false
	}
	return "shape: " + JSONShape(v) + "\n" + buf.String(), true
}
