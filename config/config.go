package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mrsirg97-rgb/rig/v2/models"
)

func Load(dir, cwd string) (*Config, error) {
	s, fileAllow, err := loadSettings(dir)
	if err != nil {
		return nil, err
	}
	t, concurrencyRetired, err := loadModels(dir)
	if err != nil {
		return nil, err
	}
	var notices []string
	if concurrencyRetired {
		notices = append(notices, fmt.Sprintf("config: %s: concurrency retired: the fleet is the resident model", filepath.Join(dir, "models.json")))
	}
	kept := make([]string, 0, len(s.Allow))
	for _, name := range s.Allow {
		if name == "plugins" {
			notices = append(notices, fmt.Sprintf("config: %s: allow names plugins, which folded into plugin in 2.8.2; the name is dropped", filepath.Join(dir, "settings.json")))
			continue
		}
		kept = append(kept, name)
	}
	s.Allow = kept
	workersRetired, err := workersRetired(dir)
	if err != nil {
		return nil, err
	}
	if workersRetired {
		notices = append(notices, fmt.Sprintf("config: %s: workers.json retired: the fleet is the resident model", filepath.Join(dir, "workers.json")))
	}
	if s.legacyJobKey {
		sp := filepath.Join(dir, "settings.json")
		notices = append(notices, fmt.Sprintf("config: %s: defaultJobModel moved to model — the fleet is the resident model; delete the key", sp))
	}
	if !fileAllow {
		s.Allow = appendWorkerTools(s.Allow)
	}
	th, err := ReadTheme(dir)
	if err != nil {
		return nil, err
	}
	ag, err := readAgents(dir, cwd)
	if err != nil {
		return nil, err
	}
	return &Config{Settings: s, Models: t, Agents: ag, Theme: th, Notices: notices}, nil
}

type Config struct {
	Settings Settings
	Models   models.Table
	Agents   string
	Theme    json.RawMessage
	Notices  []string
}

func readErr(p string, err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return fmt.Errorf("config: %s: %v", p, pe.Err)
	}
	return fmt.Errorf("config: %s: %v", p, err)
}
