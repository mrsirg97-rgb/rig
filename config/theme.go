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
	return SetKey(dir, "theme", value)
}
