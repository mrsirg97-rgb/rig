package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func ReadTheme(dir string) (json.RawMessage, error) {
	p := filepath.Join(dir, "theme.json")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, readErr(p, err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("config: %s: %v", p, err)
	}
	return json.RawMessage(data), nil
}

func SetTheme(dir string, value string) error {
	p := filepath.Join(dir, "settings.json")
	doc := map[string]any{}
	if data, err := os.ReadFile(p); err == nil {
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("config: %s: %v", p, err)
		}
	} else if !os.IsNotExist(err) {
		return readErr(p, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	doc["theme"] = value
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("config: %s: %v", p, err)
	}
	out = append(out, '\n')
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return readErr(p, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return readErr(p, err)
	}
	return nil
}
