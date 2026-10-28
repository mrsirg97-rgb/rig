package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var knownKeys = strings.Join(knownSettings, ", ")

func unknownKeyErr(key string) error {
	return fmt.Errorf("config: unknown key %q (known: %s)", key, knownKeys)
}

func noDirErr() error {
	return fmt.Errorf("config: no config dir (name the directory that holds settings.json)")
}

func ReadKey(dir, key string) (string, bool, error) {
	if dir == "" {
		return "", false, noDirErr()
	}
	if !knownSettingsSet[key] {
		return "", false, unknownKeyErr(key)
	}
	path := filepath.Join(dir, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, readErr(path, err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return "", false, readErr(path, err)
	}
	raw, ok := keys[key]
	if !ok {
		return "", false, nil
	}
	v, err := jsonString(raw)
	if err != nil {
		return "", false, readErr(path, fmt.Errorf("%s: %v", key, err))
	}
	return v, true, nil
}

func SetKey(dir, key, value string) error {
	if dir == "" {
		return noDirErr()
	}
	if !knownSettingsSet[key] {
		return unknownKeyErr(key)
	}
	path := filepath.Join(dir, "settings.json")
	doc := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &doc); err != nil {
			return readErr(path, err)
		}
	} else if !os.IsNotExist(err) {
		return readErr(path, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	doc[key] = value
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return readErr(path, err)
	}
	out = append(out, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return readErr(path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return readErr(path, err)
	}
	return nil
}
