package config

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
)

func parseSettings(data []byte, path string) (Settings, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return Settings{}, fmt.Errorf("config: %s: expected a JSON object: %w", path, err)
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
	if v, ok, err := str("ffmpeg"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.FFmpeg = v
	}
	if v, ok, err := str("browser"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.Browser = v
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
	if v, ok, err := str("decisionUrl"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.DecisionURL = v
	}
	if v, ok, err := str("decisionUnit"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.DecisionUnit = v
	}
	if v, ok, err := str("trainPython"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.TrainPython = v
	}
	if raw, ok := keys["reviewBatch"]; ok {
		v, err := jsonInt(raw)
		if err != nil {
			return Settings{}, fmt.Errorf("config: %s: reviewBatch: %v", path, err)
		}
		if v < 0 {
			return Settings{}, fmt.Errorf("config: %s: reviewBatch: expected a non-negative number, got %d", path, v)
		}
		s.ReviewBatch = &v
	}
	if v, ok, err := str("defaultJobModel"); err != nil {
		return Settings{}, err
	} else if ok {
		s.legacyJobModel = v
		s.legacyJobKey = true
	}
	if v, ok, err := str("theme"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.Theme = v
	}
	if v, ok, err := str("updateKey"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		s.UpdateKey = v
	}
	if v, ok, err := str("sandbox"); err != nil {
		return Settings{}, err
	} else if ok && v != "" {
		if v != "jailed" && v != "landlock" && v != "off" {
			return Settings{}, fmt.Errorf("config: %s: sandbox: expected \"jailed\", \"landlock\", or \"off\", got %s", path, gojson(v))
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
		v, err := jsonStringArray(raw, "sandboxBinds", "paths")
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
		if v < 0 {
			return Settings{}, fmt.Errorf("config: %s: retries: expected a non-negative number, got %d", path, v)
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
			return Settings{}, fmt.Errorf("config: %s: rounds: expected a positive number (0 = no cap), got %d", path, v)
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
	if raw, ok := keys["workers"]; ok {
		v, err := jsonBool(raw)
		if err != nil {
			return Settings{}, fmt.Errorf("config: %s: workers: %v", path, err)
		}
		s.Workers = &v
	}
	if raw, ok := keys["maxWorkers"]; ok {
		v, err := jsonInt(raw)
		if err != nil {
			return Settings{}, fmt.Errorf("config: %s: maxWorkers: %v", path, err)
		}
		if v < 0 {
			return Settings{}, fmt.Errorf("config: %s: maxWorkers: expected a non-negative number (0 = no cap), got %d", path, v)
		}
		s.MaxWorkers = v
	}
	if raw, ok := keys["workerWindow"]; ok {
		v, err := jsonInt(raw)
		if err != nil {
			return Settings{}, fmt.Errorf("config: %s: workerWindow: %v", path, err)
		}
		if v < 0 {
			return Settings{}, fmt.Errorf("config: %s: workerWindow: expected a non-negative number (0 = the row's window), got %d", path, v)
		}
		s.WorkerWindow = v
	}
	if raw, ok := keys["plugins"]; ok {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return Settings{}, fmt.Errorf("config: %s: plugins: expected an object: %w", path, err)
		}
		known := []string{"enabled", "max"}
		knownSet := map[string]bool{"enabled": true, "max": true}
		var unknown []string
		for k := range obj {
			if !knownSet[k] {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return Settings{}, fmt.Errorf("config: %s: plugins: unknown key %q (known: %s)", path, unknown[0], strings.Join(known, ", "))
		}
		if e, ok := obj["enabled"]; ok {
			v, err := jsonStringArray(e, "plugins.enabled", "names")
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
	if raw, ok := keys["mesh"]; ok {
		m, err := parseMesh(raw, path)
		if err != nil {
			return Settings{}, err
		}
		s.Mesh = &m
	}
	return s, nil
}

func parseMesh(raw json.RawMessage, path string) (Mesh, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return Mesh{}, fmt.Errorf("config: %s: mesh: expected an object", path)
	}
	known := []string{"listen", "peers", "period", "room", "window"}
	knownSet := make(map[string]bool, len(known))
	for _, k := range known {
		knownSet[k] = true
	}
	var unknown []string
	for k := range obj {
		if !knownSet[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return Mesh{}, fmt.Errorf("config: %s: mesh: unknown key %q (known: %s)", path, unknown[0], strings.Join(known, ", "))
	}
	var m Mesh
	if raw, ok := obj["room"]; ok {
		v, err := jsonString(raw)
		if err != nil {
			return Mesh{}, fmt.Errorf("config: %s: mesh.room: %v", path, err)
		}
		m.Room = v
	}
	if raw, ok := obj["listen"]; ok {
		v, err := jsonString(raw)
		if err != nil {
			return Mesh{}, fmt.Errorf("config: %s: mesh.listen: %v", path, err)
		}
		m.Listen = v
	}
	if m.Listen == "" {
		return Mesh{}, fmt.Errorf("config: %s: mesh: listen is required (the machine's door: the address the door-holder binds)", path)
	}
	host, _, err := net.SplitHostPort(m.Listen)
	if err != nil {
		return Mesh{}, fmt.Errorf("config: %s: mesh.listen: %v", path, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return Mesh{}, fmt.Errorf("config: %s: mesh.listen: %q binds every interface; the door binds one address the tailnet reaches (a wildcard bind takes the mesh port past the trust boundary)", path, m.Listen)
	}
	if raw, ok := obj["peers"]; ok {
		v, err := jsonAddresses(raw)
		if err != nil {
			return Mesh{}, fmt.Errorf("config: %s: mesh.peers: %v", path, err)
		}
		m.Peers = v
	}
	m.Period = DefaultMeshPeriod
	if raw, ok := obj["period"]; ok {
		v, err := jsonInt(raw)
		if err != nil {
			return Mesh{}, fmt.Errorf("config: %s: mesh.period: %v", path, err)
		}
		if v < 1 {
			return Mesh{}, fmt.Errorf("config: %s: mesh.period: expected a positive number (the machine's clock period in milliseconds), got %d", path, v)
		}
		m.Period = int64(v)
	}
	m.Window = DefaultMeshWindow
	if raw, ok := obj["window"]; ok {
		v, err := jsonInt(raw)
		if err != nil {
			return Mesh{}, fmt.Errorf("config: %s: mesh.window: %v", path, err)
		}
		if v < 1 {
			return Mesh{}, fmt.Errorf("config: %s: mesh.window: expected a positive number (the horizon window in ticks), got %d", path, v)
		}
		m.Window = int64(v)
	}
	return m, nil
}

func jsonAddresses(raw json.RawMessage) ([]string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected an array of door addresses, got %s", gojson(v))
	}
	out := make([]string, 0, len(arr))
	for i, el := range arr {
		str, ok := el.(string)
		if !ok {
			return nil, fmt.Errorf("peers[%d]: expected an address, got %s", i, gojson(el))
		}
		if _, _, err := net.SplitHostPort(str); err != nil {
			return nil, fmt.Errorf("peers[%d]: %v", i, err)
		}
		out = append(out, str)
	}
	return out, nil
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
	if f >= float64(math.MaxInt64) || f < float64(math.MinInt64) {
		return 0, fmt.Errorf("expected an integer within the platform range, got %s", gojson(v))
	}
	return int(f), nil
}

func jsonFloat(raw json.RawMessage) (float64, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, err
	}
	f, ok := v.(float64)
	if !ok {
		return 0, fmt.Errorf("expected a number, got %s", gojson(v))
	}
	return f, nil
}

func jsonBool(raw json.RawMessage) (bool, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("expected a boolean, got %s", gojson(v))
	}
	return b, nil
}

func jsonAllow(raw json.RawMessage) ([]string, error) {
	return jsonStringArray(raw, "allow", "tool names")
}

func jsonStringArray(raw json.RawMessage, key, what string) ([]string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected an array of %s, got %s", key, what, gojson(v))
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
