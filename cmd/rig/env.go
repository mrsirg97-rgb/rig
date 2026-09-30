package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/command"
	"github.com/mrsirg97-rgb/rig/v2/config"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/frontend/tui"
	"github.com/mrsirg97-rgb/rig/v2/middleware/approve"
	"github.com/mrsirg97-rgb/rig/v2/middleware/paths"
	"github.com/mrsirg97-rgb/rig/v2/models"
	remstore "github.com/mrsirg97-rgb/rig/v2/store/rem"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
	"github.com/mrsirg97-rgb/rig/v2/store/state"
	"github.com/mrsirg97-rgb/rig/v2/tool/file"
)

func (r *root) compactNow(ctx context.Context) (core.Compacted, bool, error) {
	ev, compacted, err := r.compactFn(ctx)
	if err != nil || !compacted {
		return ev, compacted, err
	}
	r.rec.Notify(ev)
	return ev, true, nil
}

func (r *root) newSession(ctx context.Context) (string, error) {
	if err := r.rec.Close("ok"); err != nil {
		return "", fmt.Errorf("new: %v", err)
	}

	r.effort = ""
	r.role = ""
	r.approve = r.approveDefault
	s2 := core.NewSession()
	rec2 := state.NewRecorder(r.fe, r.sdb, r.cwd, r.activeID, Version, s2.ID, s2).Snapshot(file.SnapshotFiles)
	if err := rec2.Ensure(); err != nil {
		return "", fmt.Errorf("new: %v", err)
	}
	r.swapIn(s2, rec2)
	return s2.ID, nil
}

func (r *root) sessionList(ctx context.Context) ([]command.SessionRow, error) {
	rows, err := state.ListSessions(ctx, r.sdb, state.ListCap)
	if err != nil {
		return nil, fmt.Errorf("sessions: %v", err)
	}
	out := make([]command.SessionRow, len(rows))
	for i, row := range rows {
		out[i] = command.SessionRow{ID: row.ID, Started: row.Started, Exit: row.Exit, Turns: row.Turns, Tokens: row.Tokens, Label: row.Label, Current: row.ID == r.session.ID}
	}
	return out, nil
}

func (r *root) sessionShow(ctx context.Context, id string) (string, error) {
	s, err := state.Resume(ctx, r.sdb, id)
	if err != nil {
		if errors.Is(err, state.ErrNoSuchSession) {
			return "", fmt.Errorf("sessions: no such session: %s", id)
		}
		return "", fmt.Errorf("sessions: %v", err)
	}
	return command.RenderShow(s), nil
}

func (r *root) sessionResume(ctx context.Context, id string) error {
	s, err := state.Resume(ctx, r.sdb, id)
	if err != nil {
		if errors.Is(err, state.ErrNoSuchSession) {
			return fmt.Errorf("sessions: no such session: %s", id)
		}
		return fmt.Errorf("sessions: %v", err)
	}
	if err := r.rec.Close("ok"); err != nil {
		return fmt.Errorf("sessions: %v", err)
	}
	rec2 := state.NewRecorder(r.fe, r.sdb, r.cwd, r.activeID, Version, s.ID, s).Snapshot(file.SnapshotFiles)
	if err := rec2.Ensure(); err != nil {
		return fmt.Errorf("sessions: %v", err)
	}
	r.swapIn(s, rec2)
	return nil
}

func (r *root) switchModel(ctx context.Context, id string) (string, error) {
	row, ok := r.runtime.Get(id)
	if !ok {
		return "", fmt.Errorf("models: no row for %q (known: %s)", id, strings.Join(r.runtime.Known(), ", "))
	}

	note := ""
	if r.effort != "" && !hasLevel(row.Efforts, r.effort) {
		note = fmt.Sprintf("effort: %q is not a level for %s — reset to server default", r.effort, id)
		r.effort = ""
	}
	r.row = row
	r.activeID = id
	r.applyVision()
	r.live.Set(append(r.nativeTools(), r.pluginTools...))
	provider, pol := r.buildPair()
	r.k.Provider = provider
	r.k.Policy = pol
	return note, nil
}

func firstNonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func hasLevel(levels []string, level string) bool {
	for _, l := range levels {
		if l == level {
			return true
		}
	}
	return false
}

func (r *root) effortForWire() string {
	if r.effort != "" {
		return r.effort
	}
	return r.row.Effort
}

func (r *root) switchEffort(ctx context.Context, level string) error {
	r.effort = level
	return nil
}

func (r *root) isMutating(name string) bool {
	return mutatingNatives[name] || !r.natives[name]
}

func (r *root) switchApprove(ctx context.Context, mode string) error {
	m, ok := approve.Mode(mode)
	if !ok {
		return fmt.Errorf("approve: %q is not a mode (auto, manual)", mode)
	}
	if m == approve.Manual && r.askDoor == nil {
		return errors.New("approve: manual needs an ask door (this frontend cannot ask)")
	}
	r.approve = m
	return nil
}

func (r *root) switchRole(ctx context.Context, name string) error {
	if !command.ValidRole(name) {
		return fmt.Errorf("role: %q is not a role (default, architect, reviewer)", name)
	}
	if name == "default" {
		name = ""
	}
	r.role = name
	r.fullSystem = r.buildSystem()
	provider, pol := r.buildPair()
	r.k.Provider = provider
	r.k.Policy = pol
	return nil
}

func (r *root) switchTheme(ctx context.Context, name string) error {
	if name != "warm" && name != "cool" && name != "custom" {
		return fmt.Errorf("theme: %q is not a preset (warm, cool, custom)", name)
	}
	home := r.rigHome
	if home == "" {
		h, err := rigHome()
		if err != nil {
			return err
		}
		home = h
		r.rigHome = home
	}
	doc := r.themeDoc
	if name == "custom" {
		fresh, err := config.ReadTheme(home)
		if err != nil {
			return err
		}
		if fresh == nil {
			return fmt.Errorf("theme: no theme.json in the rig home (%s)", filepath.Join(home, "theme.json"))
		}
		doc = fresh
	}
	th, err := tui.ResolveTheme(name, doc, r.themeTrueColor)
	if err != nil {
		return err
	}
	if err := config.SetTheme(home, name); err != nil {
		return err
	}
	r.theme = name
	if rp, ok := r.fe.(interface {
		RepaintTheme(tui.Theme)
	}); ok {
		rp.RepaintTheme(th)
	}
	return nil
}

func (r *root) commandEnv() *command.Env {
	workersEnv := command.Workers{File: filepath.Join(r.pluginsHome, "workers.json")}
	if r.workers != nil {
		workersEnv.Model = r.workers.Model
		workersEnv.Slots = r.workers.Slots
		workersEnv.Configured = true
	}
	return &command.Env{
		Workers:       workersEnv,
		Swarm:         swarmAdapter{r.swarm},
		Session:       func() *core.Session { return r.session },
		Compact:       r.compactNow,
		NewSession:    r.newSession,
		SessionList:   r.sessionList,
		SessionShow:   r.sessionShow,
		SessionResume: r.sessionResume,
		Models:        func() models.Table { return r.runtime },
		ActiveModel:   func() string { return r.activeID },
		SwitchModel:   r.switchModel,
		Effort:        func() string { return r.effort },
		Efforts:       func() []string { return r.row.Efforts },
		SetEffort:     r.switchEffort,
		Role:          func() string { return r.role },
		SetRole:       r.switchRole,
		Theme:         func() string { return r.theme },
		SetTheme:      r.switchTheme,
		Approve:       func() string { return r.approve },
		SetApprove:    r.switchApprove,
		Tools:         r.tools,
		Plugins:       func() []command.PluginInfo { return r.pluginInfos },
		Reload:        r.reloadPlugins,
		PluginsDir:    r.pluginsDir,
		RemList:       r.remList,
		RemShow:       r.remShow,
		RemForget:     r.remForget,
		RemLabel:      r.remLabel,
	}
}

func (r *root) remList(ctx context.Context, project string) ([]command.RemRow, error) {
	if r.remDB.DB == nil {
		return []command.RemRow{}, nil
	}
	proj := r.cwd
	if project != "" {
		proj = paths.Expand(project)
	}
	mems, err := remstore.List(ctx, r.remDB, proj, 50)
	if err != nil {
		return nil, err
	}
	out := make([]command.RemRow, len(mems))
	for i, m := range mems {
		out[i] = command.RemRow{ID: m.ID, Kind: m.Kind, ScopeLabel: m.ScopeLabel, CreatedAt: m.CreatedAt, Strength: m.Strength, Content: m.Content}
	}
	return out, nil
}

func (r *root) remShow(ctx context.Context, id int64) (command.RemRow, error) {
	if r.remDB.DB == nil {
		return command.RemRow{}, errors.New("rem: no rem store")
	}
	m, err := remstore.Show(ctx, r.remDB, id)
	if err != nil {
		if errors.Is(err, remstore.ErrNoSuchMemory) {
			return command.RemRow{}, fmt.Errorf("rem: no such memory: %d", id)
		}
		return command.RemRow{}, err
	}
	return remRow(*m), nil
}

func (r *root) remForget(ctx context.Context, id int64) error {
	if r.remDB.DB == nil {
		return errors.New("rem: no rem store")
	}
	if err := remstore.Forget(ctx, r.remDB, r.cwd, id); err != nil {
		if errors.Is(err, remstore.ErrNoSuchMemory) {
			return fmt.Errorf("rem: no such memory: %d", id)
		}
		return err
	}
	return nil
}

func (r *root) remLabel(ctx context.Context, project string) (string, error) {
	return scope.Label(paths.Expand(project)), nil
}
