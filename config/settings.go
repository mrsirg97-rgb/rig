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

type Settings struct {
	BaseURL         string
	Model           string
	System          string
	Allow           []string
	Retries         int
	Rounds          int
	ResultCap       int
	Python          string
	SearXNG         string
	WebFetchProxy   *string
	Trafilatura     *string
	SwapURL         string
	DefaultJobModel string
	Theme           string
	Sandbox         string
	SandboxBinds    []string
	Approve         string          // the approval dial's default (SPEC_MODES 4): "auto" or "manual"
	Plugins         SettingsPlugins // the plugin cap (SPEC_GROWTH 9, amended): the switch is plugins/disabled/
}

// SettingsPlugins is the operator's plugin cap: Max caps the door's name
// enum at the top Max live plugins (file order); 0 = no cap. The on/off
// switch is the directory (plugins/disabled/), never a list here.
type SettingsPlugins struct {
	Max int
}

var knownSettings = []string{"allow", "approve", "baseUrl", "defaultJobModel", "model", "plugins", "python", "resultCap", "retries", "rounds", "sandbox", "sandboxBinds", "searxngUrl", "swapUrl", "system", "theme", "trafilatura", "webFetchProxy"}

var knownSettingsSet = func() map[string]bool {
	m := make(map[string]bool, len(knownSettings))
	for _, k := range knownSettings {
		m[k] = true
	}
	return m
}()

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
			return base, nil
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
	if file.Rounds != 0 {
		out.Rounds = file.Rounds
	}
	if file.ResultCap != 0 {
		out.ResultCap = file.ResultCap
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
	if file.Approve != "" {
		out.Approve = file.Approve
	}
	if file.Plugins.Max != 0 {
		out.Plugins.Max = file.Plugins.Max
	}
	return out
}

func parseSettings(data []byte, path string) (Settings, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return Settings{}, fmt.Errorf("config: %s: expected a JSON object", path)
	}
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
	if v, ok, err := str("approve"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		if v != "auto" && v != "manual" {
			return Settings{}, fmt.Errorf("config: %s: approve: expected \"auto\" or \"manual\", got %s", path, gojson(v))
		}
		s.Approve = v
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
	if raw, ok := keys["rounds"]; ok {
		v, err := jsonInt(raw)
		if err != nil {
			return Settings{}, fmt.Errorf("config: %s: rounds: %v", path, err)
		}
		if v < 0 {
			return Settings{}, fmt.Errorf("config: %s: rounds: expected a positive number (0 = the default), got %d", path, v)
		}
		if v != 0 {
			s.Rounds = v
		}
	}
	if raw, ok := keys["resultCap"]; ok {
		v, err := jsonInt(raw)
		if err != nil {
			return Settings{}, fmt.Errorf("config: %s: resultCap: %v", path, err)
		}
		if v < 0 {
			return Settings{}, fmt.Errorf("config: %s: resultCap: expected a positive number (0 = the default), got %d", path, v)
		}
		if v != 0 {
			s.ResultCap = v
		}
	}
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
	if raw, ok := keys["plugins"]; ok {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return Settings{}, fmt.Errorf("config: %s: plugins: expected an object", path)
		}
		if e, ok := obj["enabled"]; ok {
			v, err := jsonStringArray(e, "plugins.enabled")
			if err != nil {
				return Settings{}, fmt.Errorf("config: %s: %v", path, err)
			}
			if len(v) > 0 {
				return Settings{}, fmt.Errorf("config: %s: plugins.enabled is retired (it inverted the default: enabling one hid the rest); the switch is the directory — /plugins disable <name> moves a plugin into plugins/disabled/, enable moves it back; delete the key", path)
			}
		}
		if m, ok := obj["max"]; ok {
			v, err := jsonInt(m)
			if err != nil {
				return Settings{}, fmt.Errorf("config: %s: plugins.max: %v", path, err)
			}
			if v < 0 {
				return Settings{}, fmt.Errorf("config: %s: plugins.max: expected a non-negative number, got %d", path, v)
			}
			s.Plugins.Max = v
		}
	}
	return s, nil
}

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
