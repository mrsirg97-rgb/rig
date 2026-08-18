// Package config is the runtime's config loading (SPEC_CONFIG): the
// knobs that are today flags, env vars, and constants become a
// four-layer resolution with a user file in it, and the models table
// leaves code for a file.
//
// config/ parses; the root (cmd/rig) consumes. Stdlib plus models,
// nothing else — no core, no store types (decision 1). JSON only,
// stdlib encoding/json (decision 1: a YAML dep is rejected, named).
package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mrsirg97-rgb/rig/models"
)

// Load reads the user files under dir (the config home's rig directory,
// e.g. ~/.config/rig) and the AGENTS.md pair (dir + cwd), each merged
// over its embedded default. Absent files are silent (3). Present-but-
// malformed or unreadable files refuse loud, naming the file and, for
// JSON, the field (3). The read order is fixed — the first malformed
// file wins, deterministically — settings.json, models.json,
// theme.json, AGENTS.md (global, then project) (3). Load never
// creates a file.
func Load(dir, cwd string) (*Config, error) {
	s, err := loadSettings(dir)
	if err != nil {
		return nil, err
	}
	t, err := loadModels(dir)
	if err != nil {
		return nil, err
	}
	th, err := readTheme(dir)
	if err != nil {
		return nil, err
	}
	ag, err := readAgents(dir, cwd)
	if err != nil {
		return nil, err
	}
	return &Config{Settings: s, Models: t, Agents: ag, Theme: th}, nil
}

// Config is the load's result: the file-over-embedded settings (the
// root applies the flag and env layers above, decision 2), the merged
// model table (4), the assembled AGENTS.md pair (6), and the raw theme
// document (7: the loader reads it, 10 defines it).
type Config struct {
	Settings Settings
	Models   models.Table
	Agents   string
	Theme    json.RawMessage
}

// readErr is the voice for a present-but-unreadable file (3): the OS
// reason, the path named once.
func readErr(p string, err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return fmt.Errorf("config: %s: %v", p, pe.Err)
	}
	return fmt.Errorf("config: %s: %v", p, err)
}
