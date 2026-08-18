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

// rowDoc is one parsed model row: the fields the operator set, each
// optional; the merge applies them over the embedded row (or requires
// the numbers on a new row) (4).
type rowDoc struct {
	n          int // 1-based row number in its file (the voice names it)
	id         string
	window     *int
	maxTokens  *int
	reserve    *int
	keepRecent *int
	role       *string
	effort     *string
}

// knownRowKeys is the known key set for one row, sorted: the unknown-
// key refusal names the sorted list (3).
var knownRowKeys = []string{"effort", "id", "keepRecent", "maxTokens", "reserve", "role", "window"}

var knownRowKeysSet = func() map[string]bool {
	m := make(map[string]bool, len(knownRowKeys))
	for _, k := range knownRowKeys {
		m[k] = true
	}
	return m
}()

// loadModels is the table out of code (4): the embedded 0.2.0 rows
// (config/models.json), with the user file's rows merged over them —
// per-field overlay on an embedded id, new rows added with their
// defaults, unlisted embedded rows kept. The merged table is built
// through models.New (each row checked, duplicates refused); a
// post-merge invariant error names the row id and the clause (the
// models.Check voice), with the file (3).
func loadModels(dir string) (models.Table, error) {
	embeddedData, err := embedded.ReadFile("models.json")
	if err != nil {
		panic("config: embedded models.json: " + err.Error())
	}
	embDocs, err := parseRows(embeddedData, "config/models.json (embedded)")
	if err != nil {
		return models.Table{}, err
	}
	// the embedded rows materialize over an empty table: the file's
	// numbers, the field defaults (role interactive, the policy's
	// medium) (4).
	embTable, err := mergeRows(models.Table{}, embDocs, "config/models.json (embedded)")
	if err != nil {
		return models.Table{}, err
	}
	p := filepath.Join(dir, "models.json")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return embTable, nil // absent is silent (3)
		}
		return models.Table{}, readErr(p, err)
	}
	userDocs, err := parseRows(data, p)
	if err != nil {
		return models.Table{}, err
	}
	return mergeRows(embTable, userDocs, p)
}

// parseRows decodes one model document (embedded or user) into row
// docs. Row errors name the row (1-based) and the field (3); unknown
// row keys refuse naming the known list (3).
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
		// the numeric fields, in a fixed order: deterministic when a row
		// carries several mistakes.
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
		out = append(out, d)
	}
	// a duplicate id in the same file refuses at the second occurrence
	// (the file is an overlay, so an embedded id listed twice is the
	// mistake, not the overlay).
	seen := map[string]bool{}
	for _, d := range out {
		if seen[d.id] {
			return nil, rowErr(d.n, "duplicate id %q", d.id)
		}
		seen[d.id] = true
	}
	return out, nil
}

// mergeRows overlays the user docs over the table (4): per-field overlay
// on an id the table has (each field the operator set replaces the
// table's value, each unset field keeps it); a new id requires its
// numbers and takes the defaults (role interactive, effort ""); the
// table's unlisted rows are kept. The overlay's zero-means-unset has one
// named cost: a zero numeric value is unreachable by overlay on a
// table id (the spec names it as the cost of the rule).
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
		}
		rows = append(rows, m)
	}
	rows = append(rows, added...)
	t2, err := models.New(rows...)
	if err != nil {
		// the violation is in a merged row, and the merged row's file is
		// the one the operator wrote (3): the models.Check voice, with
		// the file.
		return models.Table{}, fmt.Errorf("config: %s: %s", path, strings.TrimPrefix(err.Error(), "models: "))
	}
	return t2, nil
}
