package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mrsirg97-rgb/rig/models"
)

type rowDoc struct {
	n          int
	id         string
	window     *int
	maxTokens  *int
	reserve    *int
	keepRecent *int
	role       *string
	effort     *string
	efforts    *[]string
}

var knownRowKeys = []string{"effort", "efforts", "id", "keepRecent", "maxTokens", "reserve", "role", "window"}

var knownRowKeysSet = func() map[string]bool {
	m := make(map[string]bool, len(knownRowKeys))
	for _, k := range knownRowKeys {
		m[k] = true
	}
	return m
}()

func loadModels(dir string) (models.Table, error) {
	embeddedData, err := embedded.ReadFile("models.json")
	if err != nil {
		panic("config: embedded models.json: " + err.Error())
	}
	embDocs, err := parseRows(embeddedData, "config/models.json (embedded)")
	if err != nil {
		return models.Table{}, err
	}
	embTable, err := mergeRows(models.Table{}, embDocs, "config/models.json (embedded)")
	if err != nil {
		return models.Table{}, err
	}
	p := filepath.Join(dir, "models.json")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return embTable, nil
		}
		return models.Table{}, readErr(p, err)
	}
	userDocs, err := parseRows(data, p)
	if err != nil {
		return models.Table{}, err
	}
	return mergeRows(embTable, userDocs, p)
}

func parseRows(data []byte, path string) ([]rowDoc, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("config: %s: expected a JSON array of model rows", path)
	}
	rowErr := func(n int, format string, a ...any) error {
		return fmt.Errorf("config: %s: row %d: %s", path, n, fmt.Sprintf(format, a...))
	}
	out := make([]rowDoc, 0, len(raws))
	for i, raw := range raws {
		n := i + 1
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(raw, &keys); err != nil {
			return nil, rowErr(n, "expected a JSON object")
		}
		var unknown []string
		for k := range keys {
			if !knownRowKeysSet[k] {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return nil, rowErr(n, "unknown key %q (known: %s)", unknown[0], strings.Join(knownRowKeys, ", "))
		}
		d := rowDoc{n: n}
		if rawID, ok := keys["id"]; ok {
			v, err := jsonString(rawID)
			if err != nil {
				return nil, rowErr(n, "id: %v", err)
			}
			if v == "" {
				return nil, rowErr(n, "%q is required", "id")
			}
			d.id = v
		} else {
			return nil, rowErr(n, "%q is required", "id")
		}
		for _, field := range []string{"window", "maxTokens", "reserve", "keepRecent"} {
			rawV, ok := keys[field]
			if !ok {
				continue
			}
			v, err := jsonInt(rawV)
			if err != nil {
				return nil, rowErr(n, "%s: %v", field, err)
			}
			switch field {
			case "window":
				d.window = &v
			case "maxTokens":
				d.maxTokens = &v
			case "reserve":
				d.reserve = &v
			case "keepRecent":
				d.keepRecent = &v
			}
		}
		if rawRole, ok := keys["role"]; ok {
			v, err := jsonString(rawRole)
			if err != nil {
				return nil, rowErr(n, "role: %v", err)
			}
			if v != models.RoleInteractive && v != models.RoleWorker {
				return nil, rowErr(n, "role: %q (allowed: interactive, worker)", v)
			}
			d.role = &v
		}
		if rawEffort, ok := keys["effort"]; ok {
			v, err := jsonString(rawEffort)
			if err != nil {
				return nil, rowErr(n, "effort: %v", err)
			}
			d.effort = &v
		}
		if rawEfforts, ok := keys["efforts"]; ok {
			var list []string
			if err := json.Unmarshal(rawEfforts, &list); err != nil {
				return nil, rowErr(n, "efforts: expected a JSON array of level names")
			}
			d.efforts = &list
		}
		out = append(out, d)
	}
	seen := map[string]bool{}
	for _, d := range out {
		if seen[d.id] {
			return nil, rowErr(d.n, "duplicate id %q", d.id)
		}
		seen[d.id] = true
	}
	return out, nil
}

func mergeRows(t models.Table, docs []rowDoc, path string) (models.Table, error) {
	var added []models.Model
	overlay := map[string]*rowDoc{}
	for i := range docs {
		d := &docs[i]
		if _, ok := t.Get(d.id); ok {
			overlay[d.id] = d
			continue
		}
		m := models.Model{ID: d.id, Role: models.RoleInteractive}
		if d.window == nil {
			return models.Table{}, fmt.Errorf("config: %s: row %d: %q is required", path, d.n, "window")
		}
		m.Window = *d.window
		if d.maxTokens == nil {
			return models.Table{}, fmt.Errorf("config: %s: row %d: %q is required", path, d.n, "maxTokens")
		}
		m.MaxTokens = *d.maxTokens
		if d.reserve == nil {
			return models.Table{}, fmt.Errorf("config: %s: row %d: %q is required", path, d.n, "reserve")
		}
		m.Reserve = *d.reserve
		if d.keepRecent == nil {
			return models.Table{}, fmt.Errorf("config: %s: row %d: %q is required", path, d.n, "keepRecent")
		}
		m.KeepRecent = *d.keepRecent
		if d.role != nil {
			m.Role = *d.role
		}
		if d.effort != nil {
			m.Effort = *d.effort
		}
		if d.efforts != nil {
			m.Efforts = append([]string(nil), *d.efforts...)
		}
		added = append(added, m)
	}
	rows := make([]models.Model, 0, len(t.Known())+len(added))
	for _, id := range t.Known() {
		m, _ := t.Get(id)
		if d, ok := overlay[id]; ok {
			if d.window != nil {
				m.Window = *d.window
			}
			if d.maxTokens != nil {
				m.MaxTokens = *d.maxTokens
			}
			if d.reserve != nil {
				m.Reserve = *d.reserve
			}
			if d.keepRecent != nil {
				m.KeepRecent = *d.keepRecent
			}
			if d.role != nil {
				m.Role = *d.role
			}
			if d.effort != nil {
				m.Effort = *d.effort
			}
			if d.efforts != nil {
				m.Efforts = append([]string(nil), *d.efforts...)
			}
		}
		rows = append(rows, m)
	}
	rows = append(rows, added...)
	t2, err := models.New(rows...)
	if err != nil {
		return models.Table{}, fmt.Errorf("config: %s: %s", path, strings.TrimPrefix(err.Error(), "models: "))
	}
	return t2, nil
}
