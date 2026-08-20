package models

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	RoleInteractive = "interactive"
	RoleWorker      = "worker"
)

type Model struct {
	ID         string
	Window     int
	MaxTokens  int
	Reserve    int
	KeepRecent int
	Role       string
	Effort     string
	Efforts    []string // the model's available effort levels, in its own vocabulary and order (SPEC_MODES 1)
}

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
	if m.Role != RoleInteractive && m.Role != RoleWorker {
		return fmt.Errorf("models: %s: role: %q (allowed: interactive, worker)", m.ID, m.Role)
	}
	return nil
}

type Table struct{ rows map[string]Model }

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

func (t Table) Get(id string) (Model, bool) {
	m, ok := t.rows[id]
	return m, ok
}

func (t Table) Known() []string {
	out := make([]string, 0, len(t.rows))
	for id := range t.rows {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func overlay(m Model, env func(string) (string, bool)) (Model, error) {
	for key, set := range map[string]func(int){
		"RIG_MODEL_WINDOW":      func(n int) { m.Window = n },
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
	return m, nil
}

func Resolve(t Table, id string, env func(string) (string, bool)) (Model, error) {
	if m, ok := t.Get(id); ok {
		overlaid, err := overlay(m, env)
		if err != nil {
			return Model{}, err
		}
		if err := overlaid.Check(); err != nil {
			return Model{}, err
		}
		return overlaid, nil
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
	m := Model{ID: id, Window: window, MaxTokens: 8192, Reserve: window / 8, KeepRecent: window / 4, Role: RoleInteractive}
	if m, err = overlay(m, env); err != nil {
		return Model{}, err
	}
	if err := m.Check(); err != nil {
		return Model{}, err
	}
	return m, nil
}
