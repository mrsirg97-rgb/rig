package config

import (
	"os"
	"path/filepath"
)

const DefaultReviewBatch = 3

type Settings struct {
	BaseURL       string
	Model         string
	System        string
	Allow         []string
	Retries       int
	Rounds        int
	ResultCap     int
	Python        string
	SearXNG       string
	WebFetchProxy *string
	Trafilatura   *string
	Workers       *bool
	SwapURL       string
	DecisionURL   string
	DecisionUnit  string
	TrainPython   string
	ReviewBatch   *int
	Theme         string
	Sandbox       string
	SandboxBinds  []string
	Approve       string
	Plugins       SettingsPlugins
	UpdateKey     string

	legacyJobModel string
	legacyJobKey   bool
}

type SettingsPlugins struct {
	Max int
}

var knownSettings = []string{"allow", "approve", "baseUrl", "decisionUnit", "decisionUrl", "defaultJobModel", "model", "plugins", "python", "reviewBatch", "resultCap", "retries", "rounds", "sandbox", "sandboxBinds", "searxngUrl", "swapUrl", "system", "theme", "trafilatura", "trainPython", "updateKey", "webFetchProxy", "workers"}

var knownSettingsSet = func() map[string]bool {
	m := make(map[string]bool, len(knownSettings))
	for _, k := range knownSettings {
		m[k] = true
	}
	return m
}()

func loadSettings(dir string) (Settings, bool, error) {
	embeddedData, err := embedded.ReadFile("settings.json")
	if err != nil {
		panic("config: embedded settings.json: " + err.Error())
	}
	base, err := parseSettings(embeddedData, "config/settings.json (embedded)")
	if err != nil {
		return Settings{}, false, err
	}
	p := filepath.Join(dir, "settings.json")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return base, false, nil
		}
		return Settings{}, false, readErr(p, err)
	}
	file, err := parseSettings(data, p)
	if err != nil {
		return Settings{}, false, err
	}
	out := mergeSettings(base, file)
	out.legacyJobModel, out.legacyJobKey = file.legacyJobModel, file.legacyJobKey
	return out, len(file.Allow) > 0, nil
}

func (s Settings) ReviewBatchOrDefault() int {
	if s.ReviewBatch == nil {
		return DefaultReviewBatch
	}
	return *s.ReviewBatch
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
	if file.DecisionURL != "" {
		out.DecisionURL = file.DecisionURL
	}
	if file.DecisionUnit != "" {
		out.DecisionUnit = file.DecisionUnit
	}
	if file.TrainPython != "" {
		out.TrainPython = file.TrainPython
	}
	if file.ReviewBatch != nil {
		out.ReviewBatch = file.ReviewBatch
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
	if file.UpdateKey != "" {
		out.UpdateKey = file.UpdateKey
	}
	if file.Workers != nil {
		out.Workers = file.Workers
	}
	return out
}
