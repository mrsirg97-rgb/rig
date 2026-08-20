package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Settings: the existing knobs by their env names, lowerCamel (RIG_
// BASE_URL -> baseUrl), plus defaultJobModel (no env in 0.2.0; file over
// embedded only) (5). WebFetchProxy and Trafilatura are presence-aware:
// their empty value is a choice (direct egress, the stdlib text pass) —
// 0.2.0's documented "set empty" env semantics, extended to the file
// layer (2, 5).
type Settings struct {
	BaseURL         string
	Model           string
	System          string
	Allow           []string
	Retries         int
	Python          string
	SearXNG         string
	WebFetchProxy   *string
	Trafilatura     *string // nil = auto (shared venv, then PATH)
	SwapURL         string
	DefaultJobModel string
	Theme           string // shipped theme name; "" = the TUI's default
	Sandbox         string // the worker jail's profile: "jailed" (default) or "off" (SPEC_SANDBOX 5)
	SandboxBinds    []string
}

// knownSettings is the known key set, sorted: the unknown-key refusal
// names the sorted list (3).
var knownSettings = []string{"allow", "baseUrl", "defaultJobModel", "model", "python", "retries", "sandbox", "sandboxBinds", "searxngUrl", "swapUrl", "system", "theme", "trafilatura", "webFetchProxy"}

var knownSettingsSet = func() map[string]bool {
	m := make(map[string]bool, len(knownSettings))
	for _, k := range knownSettings {
		m[k] = true
	}
	return m
}()

// loadSettings is the settings chain's file-over-embedded layer (2):
// the embedded 0.2.0 defaults, with the user file's set keys over them.
// Zero means unset at the file layer (an empty string or zero
// descends), except the two presence-aware keys, for which present is
// set — even empty (2, 5).
func loadSettings(dir string) (Settings, error) {
	embeddedData, err := embedded.ReadFile("settings.json")
	if err != nil {
		panic("config: embedded settings.json: " + err.Error())
	}
	base, err := parseSettings(embeddedData, "config/settings.json (embedded)")
	if err != nil {
		return Settings{}, err
	}
	p := filepath.Join(dir, "settings.json")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return base, nil // absent is silent (3)
		}
		return Settings{}, readErr(p, err)
	}
	file, err := parseSettings(data, p)
	if err != nil {
		return Settings{}, err
	}
	return mergeSettings(base, file), nil
}

func mergeSettings(base, file Settings) Settings {
	out := base
	if file.BaseURL != "" {
		out.BaseURL = file.BaseURL
	}
	if file.Model != "" {
		out.Model = file.Model
	}
	if file.System != "" {
		out.System = file.System
	}
	if len(file.Allow) > 0 {
		out.Allow = file.Allow
	}
	if file.Retries != 0 {
		out.Retries = file.Retries
	}
	if file.Python != "" {
		out.Python = file.Python
	}
	if file.SearXNG != "" {
		out.SearXNG = file.SearXNG
	}
	if file.WebFetchProxy != nil {
		out.WebFetchProxy = file.WebFetchProxy
	}
	if file.Trafilatura != nil {
		out.Trafilatura = file.Trafilatura
	}
	if file.SwapURL != "" {
		out.SwapURL = file.SwapURL
	}
	if file.DefaultJobModel != "" {
		out.DefaultJobModel = file.DefaultJobModel
	}
	if file.Theme != "" {
		out.Theme = file.Theme
	}
	if file.Sandbox != "" {
		out.Sandbox = file.Sandbox
	}
	if len(file.SandboxBinds) > 0 {
		out.SandboxBinds = file.SandboxBinds
	}
	return out
}

// parseSettings decodes one settings document (embedded or user). Each
// known key is decoded individually, so a type error names the
// operator's JSON key, not a Go struct name (3); an unknown key refuses
// naming the known list (3).
func parseSettings(data []byte, path string) (Settings, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return Settings{}, fmt.Errorf("config: %s: expected a JSON object", path)
	}
	// the unknown key is the first in sorted order: deterministic, and
	// the typo'd key is named before anything it might shadow.
	var unknown []string
	for k := range keys {
		if !knownSettingsSet[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return Settings{}, fmt.Errorf("config: %s: unknown key %q (known: %s)", path, unknown[0], strings.Join(knownSettings, ", "))
	}
	var s Settings
	str := func(key string) (string, bool, error) {
		raw, ok := keys[key]
		if !ok {
			return "", false, nil
		}
		v, err := jsonString(raw)
		if err != nil {
			return "", false, fmt.Errorf("config: %s: %s: %v", path, key, err)
		}
		return v, true, nil
	}
	if v, ok, err := str("baseUrl"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.BaseURL = v
	}
	if v, ok, err := str("model"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.Model = v
	}
	if v, ok, err := str("system"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.System = v
	}
	if v, ok, err := str("python"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.Python = v
	}
	if v, ok, err := str("searxngUrl"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.SearXNG = v
	}
	if v, ok, err := str("swapUrl"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.SwapURL = v
	}
	if v, ok, err := str("defaultJobModel"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.DefaultJobModel = v
	}
	if v, ok, err := str("theme"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.Theme = v
	}
	if v, ok, err := str("sandbox"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		if v != "jailed" && v != "off" {
			return Settings{}, fmt.Errorf("config: %s: sandbox: expected \"jailed\" or \"off\", got %s", path, gojson(v))
		}
		s.Sandbox = v
	}
	if raw, ok := keys["allow"]; ok {
		v, err := jsonAllow(raw)
		if err != nil {
			return Settings{}, fmt.Errorf("config: %s: %v", path, err)
		}
		if len(v) > 0 {
			s.Allow = v
		}
	}
	if raw, ok := keys["sandboxBinds"]; ok {
		v, err := jsonStringArray(raw, "sandboxBinds")
		if err != nil {
			return Settings{}, fmt.Errorf("config: %s: %v", path, err)
		}
		if len(v) > 0 {
			s.SandboxBinds = v
		}
	}
	if raw, ok := keys["retries"]; ok {
		v, err := jsonInt(raw)
		if err != nil {
			return Settings{}, fmt.Errorf("config: %s: retries: %v", path, err)
		}
		if v != 0 {
			s.Retries = v
		}
	}
	// the presence-aware keys: present is set, even empty (2, 5).
	if raw, ok := keys["webFetchProxy"]; ok {
		v, err := jsonString(raw)
		if err != nil {
			return Settings{}, fmt.Errorf("config: %s: webFetchProxy: %v", path, err)
		}
		s.WebFetchProxy = &v
	}
	if raw, ok := keys["trafilatura"]; ok {
		v, err := jsonString(raw)
		if err != nil {
			return Settings{}, fmt.Errorf("config: %s: trafilatura: %v", path, err)
		}
		s.Trafilatura = &v
	}
	return s, nil
}

// --- the per-key decoders (3: the field is the operator's spelling) ---

func jsonString(raw json.RawMessage) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	str, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("expected a string, got %s", gojson(v))
	}
	return str, nil
}

func jsonInt(raw json.RawMessage) (int, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, err
	}
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) {
		return 0, fmt.Errorf("expected an integer, got %s", gojson(v))
	}
	return int(f), nil
}

func jsonAllow(raw json.RawMessage) ([]string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("allow: expected an array of tool names, got %s", gojson(v))
	}
	out := make([]string, 0, len(arr))
	for i, el := range arr {
		str, ok := el.(string)
		if !ok {
			return nil, fmt.Errorf("allow[%d]: expected a string, got %s", i, gojson(el))
		}
		out = append(out, str)
	}
	return out, nil
}

// jsonStringArray is a named array-of-strings decoder (SPEC_SANDBOX 5's
// sandboxBinds is its user): the refusals name the operator's key.
func jsonStringArray(raw json.RawMessage, key string) ([]string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected an array of paths, got %s", key, gojson(v))
	}
	out := make([]string, 0, len(arr))
	for i, el := range arr {
		str, ok := el.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d]: expected a string, got %s", key, i, gojson(el))
		}
		out = append(out, str)
	}
	return out, nil
}

// gojson renders a JSON value the way the operator wrote it: strings
// quoted, numbers as-is — the refusal voice's "got" half (3).
func gojson(v any) string {
	switch t := v.(type) {
	case string:
		return strconv.Quote(t)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return "null"
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}
