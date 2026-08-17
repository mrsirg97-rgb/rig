// Package models is the root's per-model table (SPEC_COMPACT 2): the
// window-relative compaction numbers, one self-contained row per model.
// Stdlib only, like core: configuration the root owns and reads, not a
// wire type — deliverable 9's `models` command reads this same table.
package models

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Model is one row: the model's own window math. The row invariants make
// the pi bug (a global reserve larger than the worker's window fires
// compaction every turn) impossible by construction: the reserve is
// per-row, checked against its own row's window.
type Model struct {
	ID         string
	Window     int // context window, tokens
	MaxTokens  int // max output tokens per request
	Reserve    int // tokens held back for the response
	KeepRecent int // token budget for the kept tail
}

// Check is the row's invariants, loud, naming the id and the fields
// (SPEC_COMPACT 2). A row that violates them cannot work: Reserve >=
// Window fires the trigger at every estimate; KeepRecent >= Window -
// Reserve leaves no room for the summary beside the tail, so compaction
// can never help.
func (m Model) Check() error {
	if m.ID == "" {
		return errors.New("models: row with an empty id")
	}
	if m.Window <= 0 {
		return fmt.Errorf("models: %s: Window %d must be > 0", m.ID, m.Window)
	}
	if m.MaxTokens <= 0 {
		return fmt.Errorf("models: %s: MaxTokens %d must be > 0", m.ID, m.MaxTokens)
	}
	if m.Reserve < 0 || m.Reserve >= m.Window {
		return fmt.Errorf("models: %s: Reserve %d must be in [0, Window %d): as large as the window, the trigger fires at every estimate (the pi shape)",
			m.ID, m.Reserve, m.Window)
	}
	if m.KeepRecent < 0 || m.KeepRecent >= m.Window-m.Reserve {
		return fmt.Errorf("models: %s: KeepRecent %d must be in [0, Window-Reserve %d): the usable window must leave room for the summary beside the tail",
			m.ID, m.KeepRecent, m.Window-m.Reserve)
	}
	return nil
}

// Table is the root's per-model table, id -> row.
type Table struct{ rows map[string]Model }

// New builds a table from rows, each checked; a violating row is refused
// at construction, loud. Duplicate ids are a construction error: the
// table is the single source of the row's numbers.
func New(rows ...Model) (Table, error) {
	t := Table{rows: map[string]Model{}}
	for _, m := range rows {
		if err := m.Check(); err != nil {
			return Table{}, err
		}
		if _, dup := t.rows[m.ID]; dup {
			return Table{}, fmt.Errorf("models: duplicate row for %q", m.ID)
		}
		t.rows[m.ID] = m
	}
	return t, nil
}

// Get looks up one row.
func (t Table) Get(id string) (Model, bool) {
	m, ok := t.rows[id]
	return m, ok
}

// Known lists the table's ids in stable order, for the refusal voice.
func (t Table) Known() []string {
	out := make([]string, 0, len(t.rows))
	for id := range t.rows {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Defaults ships the worker profile under rig's default id and the
// scheduler's worker id (SPEC_COMPACT 2). The 262k brain row is one
// table entry, added by the deployment's alias or carried by env
// (Resolve).
var Defaults Table = mustNew(
	Model{ID: "local", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384},
	Model{ID: "qwen3.8-workers", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384},
)

func mustNew(rows ...Model) Table {
	t, err := New(rows...)
	if err != nil {
		panic("models: Defaults: " + err.Error())
	}
	return t
}

// Resolve is the root's row resolution at start, before any store is
// opened (SPEC_COMPACT 2): the table row for the active id; else, if
// RIG_MODEL_WINDOW is set, a synthesized row for the active id from
// RIG_MODEL_WINDOW / RIG_MODEL_MAX_TOKENS / RIG_MODEL_RESERVE /
// RIG_MODEL_KEEP_RECENT — absent fields take the named defaults
// (MaxTokens 8192, Reserve Window/8, KeepRecent Window/4) — then
// validated; else a loud refusal naming the id, the table's known ids,
// and the env. env is the lookup seam (os.LookupEnv at the root; a map in
// tests).
func Resolve(t Table, id string, env func(string) (string, bool)) (Model, error) {
	if m, ok := t.Get(id); ok {
		return m, nil
	}
	raw, ok := env("RIG_MODEL_WINDOW")
	if !ok || raw == "" {
		return Model{}, fmt.Errorf("models: no row for %q (known: %s; set RIG_MODEL_WINDOW to define one)",
			id, strings.Join(t.Known(), ", "))
	}
	window, err := strconv.Atoi(raw)
	if err != nil {
		return Model{}, fmt.Errorf("models: RIG_MODEL_WINDOW %q: %v", raw, err)
	}
	m := Model{ID: id, Window: window, MaxTokens: 8192, Reserve: window / 8, KeepRecent: window / 4}
	for key, set := range map[string]func(int){
		"RIG_MODEL_MAX_TOKENS":  func(n int) { m.MaxTokens = n },
		"RIG_MODEL_RESERVE":     func(n int) { m.Reserve = n },
		"RIG_MODEL_KEEP_RECENT": func(n int) { m.KeepRecent = n },
	} {
		raw, ok := env(key)
		if !ok || raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			return Model{}, fmt.Errorf("models: %s %q: %v", key, raw, err)
		}
		set(n)
	}
	if err := m.Check(); err != nil {
		return Model{}, err
	}
	return m, nil
}
