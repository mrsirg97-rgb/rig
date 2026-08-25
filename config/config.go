package config

import (
	"encoding/json"
	"fmt"
	"os"

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
	return &Config{Settings: s, Models: t, Workers: w, Agents: ag, Theme: th}, nil
}

type Config struct {
	Settings Settings
	Models   models.Table
	Workers  *Workers
	Agents   string
	Theme    json.RawMessage
}

func readErr(p string, err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return fmt.Errorf("config: %s: %v", p, pe.Err)
	}
	return fmt.Errorf("config: %s: %v", p, err)
}
