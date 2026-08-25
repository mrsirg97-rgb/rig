package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/models"
)

func Load(dir, cwd string) (*Config, error) {
	s, fileAllow, err := loadSettings(dir)
	if err != nil {
		return nil, err
	}
	t, err := loadModels(dir)
	if err != nil {
		return nil, err
	}
	w, err := loadWorkers(dir, t)
	if err != nil {
		return nil, err
	}
	notice := ""
	if s.legacyJobKey {
		sp := filepath.Join(dir, "settings.json")
		wp := filepath.Join(dir, "workers.json")
		switch {
		case s.legacyJobModel == "":
			notice = fmt.Sprintf("config: %s: defaultJobModel is cut (the fleet is workers.json); delete the key", sp)
		case w == nil:
			if _, ok := t.Get(s.legacyJobModel); !ok {
				return nil, fmt.Errorf("config: %s: defaultJobModel %q: no row in the models table (known: %s); add the row or delete the key", sp, s.legacyJobModel, strings.Join(t.Known(), ", "))
			}
			if err := os.WriteFile(wp, []byte(fmt.Sprintf("{\"model\": %q}\n", s.legacyJobModel)), 0o644); err != nil {
				return nil, readErr(wp, err)
			}
			w = &Workers{Model: s.legacyJobModel, Slots: 1}
			notice = fmt.Sprintf("config: %s: defaultJobModel moved to workers.json — minted %s with model %q; delete the key", sp, wp, s.legacyJobModel)
		case w.Model == s.legacyJobModel:
			notice = fmt.Sprintf("config: %s: defaultJobModel is ignored (workers.json names the fleet); delete the key", sp)
		default:
			return nil, fmt.Errorf("config: %s: defaultJobModel %q disagrees with workers.json's model %q; delete the key", sp, s.legacyJobModel, w.Model)
		}
	}
	if w != nil && !fileAllow {
		s.Allow = appendWorkerTools(s.Allow)
	}
	th, err := readTheme(dir)
	if err != nil {
		return nil, err
	}
	ag, err := readAgents(dir, cwd)
	if err != nil {
		return nil, err
	}
	return &Config{Settings: s, Models: t, Workers: w, Agents: ag, Theme: th, Notice: notice}, nil
}

type Config struct {
	Settings Settings
	Models   models.Table
	Workers  *Workers
	Agents   string
	Theme    json.RawMessage
	Notice   string
}

func readErr(p string, err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return fmt.Errorf("config: %s: %v", p, pe.Err)
	}
	return fmt.Errorf("config: %s: %v", p, err)
}
