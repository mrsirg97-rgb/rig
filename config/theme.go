package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// readTheme is the theme.json read (7): present → it must decode as one
// well-formed JSON value, else the loud refusal (3); the file's bytes
// are exposed on Config.Theme (json.RawMessage), verbatim as written —
// 10 (SPEC_TUI) owns the palette schema and decodes the raw value.
// Absent → nil, silent (3). The loader validates well-formedness only —
// no fields, no keys, no schema: the moment it named a field it would
// own 10's territory (7).
func readTheme(dir string) (json.RawMessage, error) {
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
