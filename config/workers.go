package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mrsirg97-rgb/rig/models"
)

type Workers struct {
	Model string
	Slots int
}

var workerToolNames = []string{"scheduler", "delegate"}

func appendWorkerTools(allow []string) []string {
	set := make(map[string]bool, len(allow))
	for _, n := range allow {
		set[n] = true
	}
	out := append([]string{}, allow...)
	for _, n := range workerToolNames {
		if !set[n] {
			out = append(out, n)
		}
	}
	return out
}

func loadWorkers(dir string, t models.Table) (*Workers, error) {
	p := filepath.Join(dir, "workers.json")
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, readErr(p, err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return nil, fmt.Errorf("config: %s: expected a JSON object", p)
	}
	var unknown []string
	for k := range keys {
		if k != "model" && k != "slots" {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("config: %s: unknown key %q (known: model, slots)", p, unknown[0])
	}
	w := &Workers{Slots: 1}
	modelSet := false
	if raw, ok := keys["model"]; ok {
		var m string
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("config: %s: model: expected a string, got %s", p, string(raw))
		}
		if m == "" {
			return nil, fmt.Errorf("config: %s: model: expected a non-empty string, got the empty string", p)
		}
		w.Model = m
		modelSet = true
	}
	if !modelSet {
		return nil, fmt.Errorf("config: %s: \"model\" is required", p)
	}
	if raw, ok := keys["slots"]; ok {
		var n int
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("config: %s: slots: expected an integer, got %s", p, string(raw))
		}
		if n < 1 {
			return nil, fmt.Errorf("config: %s: slots: expected a positive number, got %d", p, n)
		}
		w.Slots = n
	}
	if _, ok := t.Get(w.Model); !ok {
		return nil, fmt.Errorf("config: %s: model %q: no row in the models table (known: %s)", p, w.Model, strings.Join(t.Known(), ", "))
	}
	return w, nil
}
