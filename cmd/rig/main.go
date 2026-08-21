package main

import (
	"encoding/json"
	"errors"
	"path/filepath"

	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mrsirg97-rgb/rig"
	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/config"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/frontend/cli"
	"github.com/mrsirg97-rgb/rig/frontend/oneshot"
	"github.com/mrsirg97-rgb/rig/frontend/tui"
	"github.com/mrsirg97-rgb/rig/loop"
	"github.com/mrsirg97-rgb/rig/middleware/approve"
	"github.com/mrsirg97-rgb/rig/middleware/guard"
	"github.com/mrsirg97-rgb/rig/middleware/perm"
	"github.com/mrsirg97-rgb/rig/middleware/toolset"
	"github.com/mrsirg97-rgb/rig/models"
	"github.com/mrsirg97-rgb/rig/plugins"
	"github.com/mrsirg97-rgb/rig/policy/compact"
	effort "github.com/mrsirg97-rgb/rig/policy/effort"
	"github.com/mrsirg97-rgb/rig/provider/openai"
	"github.com/mrsirg97-rgb/rig/store"
	remstore "github.com/mrsirg97-rgb/rig/store/rem"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	"github.com/mrsirg97-rgb/rig/store/state"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
	"github.com/mrsirg97-rgb/rig/tool/bash"
	"github.com/mrsirg97-rgb/rig/tool/diff"
	"github.com/mrsirg97-rgb/rig/tool/file"
	"github.com/mrsirg97-rgb/rig/tool/fs"
	pythontool "github.com/mrsirg97-rgb/rig/tool/python"
	remapi "github.com/mrsirg97-rgb/rig/tool/rem"
	schedapi "github.com/mrsirg97-rgb/rig/tool/scheduler"
	todoapi "github.com/mrsirg97-rgb/rig/tool/todo"
	webtool "github.com/mrsirg97-rgb/rig/tool/web"
)

const Version = "0.9.1"

type root struct {
	baseURL string
	system  string
	agents  string
	allow   []string
	retries int

	middleware []core.ToolMiddleware

	fe    core.Frontend
	sdb   store.DB
	remDB store.DB
	cwd   string

	pluginsDir string

	activeID string
	row      models.Model
	runtime  models.Table

	effort string
	role   string

	approve        string
	approveDefault string
	askDoor        func(ctx context.Context, prompt string) bool

	session *core.Session
	rec     *state.Recorder
	tools   map[string]core.Tool

	pluginTools []core.Tool

	live *toolset.Table

	natives map[string]bool

	py plugins.Kernel

	pluginsHome string

	pluginInfos []command.PluginInfo

	fullSystem string
	k          *rig.Kernel

	compactFn func(ctx context.Context) (core.Compacted, bool, error)
}

func wire(r *root) *rig.Kernel {

	if r.live == nil {
		// The door tools hold the table as their Live seam (SPEC_GROWTH 9);
		// build the empty table first, construct the doors over it, then
		// fill it from the natives (the doors among them) and the plugins.
		r.live = toolset.New()
		if r.tools["plugin"] == nil {
			r.tools["plugin"] = plugins.NewDoor(r.live)
			r.tools["plugin_schema"] = plugins.NewSchemaDoor(r.live)
		}
		r.live.Set(append(r.nativeTools(), r.pluginTools...))
	}
	r.live.SetPlugins(r.pluginNames()...)
	if r.natives == nil {
		r.natives = make(map[string]bool, len(nativeToolNames))
		for _, name := range nativeToolNames {
			r.natives[name] = true
		}
	}
	mw := r.middleware
	if mw == nil {

		mw = []core.ToolMiddleware{
			toolset.Resolve(r.live),
		}

		if r.askDoor != nil {
			mw = append(mw, approve.Gate(func() string { return r.approve }, r.askDoor, r.isMutating))
		}
		mw = append(mw,
			perm.Plugins(r.pluginsDir),
			perm.AllowlistWithDoor(r.allow, r.pluginDoor()),
			guard.Bound(r.retries),
		)
	}

	r.fullSystem = r.buildSystem()
	provider, pol := r.buildPair()
	k := rig.New(
		rig.WithProvider(provider),
		rig.WithFrontend(r.rec),
		rig.WithPolicy(pol),
		rig.WithTools(append(
			r.nativeTools(),
			r.pluginTools...,
		)...),
		rig.WithMiddleware(mw...),
	)
	k.Session = r.session
	r.k = k
	return k
}

func (r *root) buildSystem() string {
	mw := r.middleware
	if mw == nil {
		mw = []core.ToolMiddleware{
			perm.Plugins(r.pluginsDir),
			perm.AllowlistWithDoor(r.allow, r.pluginDoor()),
			guard.Bound(r.retries),
		}
	}
	parts := make([]string, 0, 5)
	if r.system != "" {
		parts = append(parts, r.system)
	}
	if seg := command.RoleProse(r.role); seg != "" {
		parts = append(parts, seg)
	}
	if r.agents != "" {
		parts = append(parts, r.agents)
	}
	if seg := r.remembered(); seg != "" {
		parts = append(parts, seg)
	}
	if g := guidelinesOf(mw); g != "" {
		parts = append(parts, g)
	}
	return strings.Join(parts, "\n\n")
}

const rememberedK = 8

func (r *root) remembered() string {
	if r.remDB.DB == nil || r.cwd == "" {
		return ""
	}
	notes, err := remstore.Recent(context.Background(), r.remDB, r.cwd, rememberedK)
	if err != nil || len(notes) == 0 {
		return ""
	}
	return renderRemembered(notes)
}

func renderRemembered(notes []string) string {
	const capChars = 1500
	header := "remembered (this directory):"
	var lines []string
	for _, n := range notes {
		if n = strings.Join(strings.Fields(n), " "); n != "" {
			lines = append(lines, "- "+n)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	chars := func(s string) int { return len([]rune(s)) }
	seg := func(l []string) string { return header + "\n" + strings.Join(l, "\n") }
	if chars(seg(lines)) <= capChars {
		return seg(lines)
	}
	for len(lines) > 1 && chars(seg(lines)) > capChars {
		lines = lines[:len(lines)-1]
	}
	if chars(seg(lines)) <= capChars {
		return seg(lines)
	}

	budget := capChars - chars(header) - 1 - 2 - 1
	line := []rune(strings.TrimPrefix(lines[0], "- "))
	if len(line) > budget {
		line = line[:budget]
	}
	return header + "\n- " + string(line) + "…"
}

func (r *root) nativeTools() []core.Tool {
	out := make([]core.Tool, 0, len(nativeToolNames))
	for _, name := range nativeToolNames {
		out = append(out, r.tools[name])
	}
	return out
}

func (r *root) buildPair() (core.Provider, core.ContextPolicy) {
	inner := openai.New(r.baseURL, r.activeID)
	opts := []compact.Option{}
	if r.remDB.DB != nil && r.cwd != "" {
		opts = append(opts, compact.WithAutoReflect(func(ctx context.Context, summary string) error {
			_, err := remstore.AutoReflect(ctx, r.remDB, r.cwd, summary)
			return err
		}))
	}
	pol, err := compact.New(inner, r.rec, r.session, r.fullSystem, r.row, opts...)
	if err != nil {
		panic("rig: wire: " + err.Error())
	}
	r.compactFn = pol.Compact

	effInner := effort.Decorator(inner, r.effortForWire)

	return toolset.Carry(r.live, compact.Decorator(effInner, pol)), pol
}

func (r *root) swapIn(s *core.Session, rec2 *state.Recorder) {
	r.rec.Retarget(s.ID, s)
	r.rec = rec2
	r.session = s
	r.k.Frontend = rec2
	r.k.Session = s

	r.fullSystem = r.buildSystem()
	provider, pol := r.buildPair()
	r.k.Provider = provider
	r.k.Policy = pol
}

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
	rec2 := state.NewRecorder(r.fe, r.sdb, r.cwd, r.activeID, Version, s2.ID, s2)
	if err := rec2.Ensure(); err != nil {
		return "", fmt.Errorf("new: %v", err)
	}
	r.swapIn(s2, rec2)
	return s2.ID, nil
}

func (r *root) sessionList(ctx context.Context) ([]command.SessionRow, error) {
	rows, err := state.ListSessions(ctx, r.sdb)
	if err != nil {
		return nil, fmt.Errorf("sessions: %v", err)
	}
	out := make([]command.SessionRow, len(rows))
	for i, row := range rows {
		out[i] = command.SessionRow{ID: row.ID, Started: row.Started, Exit: row.Exit, Turns: row.Turns, Current: row.ID == r.session.ID}
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
	rec2 := state.NewRecorder(r.fe, r.sdb, r.cwd, r.activeID, Version, s.ID, s)
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

var mutatingNatives = map[string]bool{
	"bash": true, "write": true, "edit": true, "python": true,
	"scheduler": true, "plugins_reload": true,
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

func (r *root) swapPlugins(ctx context.Context, reports []plugins.Report) (string, error) {
	infos := make([]command.PluginInfo, 0, len(reports))
	tools := r.nativeTools()
	for _, rep := range reports {
		infos = append(infos, command.PluginInfo{
			Name: rep.Name, Description: rep.Description, File: rep.File,
			Skipped: rep.Skipped, Reason: rep.Reason,
		})
		if !rep.Skipped {
			tools = append(tools, plugins.New(rep.Name, rep.Description, rep.File, rep.Schema, r.py))
		}
	}
	names := make([]string, 0, len(reports))
	for _, rep := range reports {
		if !rep.Skipped {
			names = append(names, rep.Name)
		}
	}
	r.live.Set(tools)
	r.live.SetPlugins(names...)
	r.pluginInfos = infos
	return command.RenderPlugins(infos, "reload"), nil
}

// pluginNames are the names of the startup-discovered plugins (the
// seed; reloads own the live table's plugin set afterwards).
func (r *root) pluginNames() []string {
	out := make([]string, 0, len(r.pluginTools))
	for _, t := range r.pluginTools {
		out = append(out, t.Name())
	}
	return out
}

// pluginDoor is the allow-list's second door over the live plugin
// table: it admits a currently-live plugin only, never a native (the
// collision rule keeps the sets disjoint). A nil live table answers
// nothing — the nil-door, today-only behavior.
func (r *root) pluginDoor() func(string) bool {
	return func(name string) bool {
		return r.live != nil && r.live.IsPlugin(name)
	}
}

func (r *root) reloadPlugins(ctx context.Context) (string, error) {
	files, err := plugins.List(r.pluginsHome)
	if err != nil {
		return "", fmt.Errorf("plugins: reload: %v", err)
	}
	reports := make([]plugins.Report, 0)
	if len(files) > 0 {
		reports, err = plugins.Discover(ctx, r.py, files)
		if err != nil {
			return "", fmt.Errorf("plugins: reload: %v", err)
		}
	}
	if err := plugins.Check(reports, r.natives); err != nil {
		return "", err
	}
	return r.swapPlugins(ctx, reports)
}

// setPluginEnabled toggles a plugin's enablement (SPEC_GROWTH 9): edits
// settings.json's plugins.enabled array (preserving the rest of the file),
// then reloads — the next-turn semantics, exactly.
func (r *root) setPluginEnabled(ctx context.Context, name string, enabled bool) (string, error) {
	path := filepath.Join(r.pluginsHome, "settings.json")
	raw := []byte("{}")
	if data, err := os.ReadFile(path); err == nil {
		raw = data
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("plugins: enable: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", fmt.Errorf("plugins: enable: %s: %v", path, err)
	}
	pl, _ := obj["plugins"].(map[string]any)
	if pl == nil {
		pl = map[string]any{}
	}
	enabledList, _ := pl["enabled"].([]any)
	if enabled {
		present := false
		for _, n := range enabledList {
			if n == name {
				present = true
			}
		}
		if !present {
			enabledList = append(enabledList, name)
			pl["enabled"] = enabledList
		}
	} else {
		filtered := make([]any, 0, len(enabledList))
		for _, n := range enabledList {
			if n != name {
				filtered = append(filtered, n)
			}
		}
		pl["enabled"] = filtered
	}
	obj["plugins"] = pl
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return "", fmt.Errorf("plugins: enable: %v", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", fmt.Errorf("plugins: enable: %v", err)
	}
	verb := "enabled"
	if !enabled {
		verb = "disabled"
	}
	line := fmt.Sprintf("plugins: %s %s", verb, name)
	reply, err := r.reloadPlugins(ctx)
	if err != nil {
		return "", fmt.Errorf("%s; the reload failed: %v", line, err)
	}
	return line + "\n" + reply, nil
}

func runtimeTable(t models.Table, active string, resolved models.Model) models.Table {
	if base, ok := t.Get(active); ok && reflect.DeepEqual(base, resolved) {
		return t
	}
	rows := make([]models.Model, 0, len(t.Known())+1)
	for _, id := range t.Known() {
		if id == active {
			continue
		}
		m, _ := t.Get(id)
		rows = append(rows, m)
	}
	rows = append(rows, resolved)
	t2, err := models.New(rows...)
	if err != nil {
		panic("rig: runtime table: " + err.Error())
	}
	return t2
}

func guidelinesOf(ms []core.ToolMiddleware) string {
	var b strings.Builder
	for _, mw := range ms {
		if gc, ok := mw.(core.GuidelineContributor); ok {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(gc.Guidelines())
		}
	}
	return b.String()
}

var ErrResumeWithPrompt = errors.New("rig: -resume is not available with -p (one-shot stays one-shot)")

func checkOneShot(prompt, resumeID string) error {
	if prompt != "" && resumeID != "" {
		return ErrResumeWithPrompt
	}
	return nil
}

func userHome() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}

var nativeToolNames = []string{"bash", "read", "write", "edit", "ls", "find", "grep", "todo", "rem", "scheduler", "python", "web_search", "web_fetch", "diff", "plugin", "plugin_schema", "plugins_reload"}

func rigHome() (string, error) {
	if v := os.Getenv("RIG_HOME"); v != "" {
		return v, nil
	}
	if h := userHome(); h == "" {
		return "", errors.New("cannot resolve the home directory (set $HOME or RIG_HOME)")
	} else {
		newHome := filepath.Join(h, ".rig")
		oldHome := filepath.Join(h, ".config", "rig")
		if fi, err := os.Stat(oldHome); err == nil && fi.IsDir() {
			if _, err := os.Stat(newHome); errors.Is(err, os.ErrNotExist) {
				if err := os.Rename(oldHome, newHome); err != nil {
					return "", fmt.Errorf("migrate the config home: %s -> %s: %v", oldHome, newHome, err)
				}
				fmt.Fprintf(os.Stderr, "rig: migrated the config home: %s -> %s\n", oldHome, newHome)
			} else if err == nil {

				fmt.Fprintf(os.Stderr, "rig: the old config home still exists: %s (the home here won: %s; merge or prune it by hand)\n", oldHome, newHome)
			}
		}
		return newHome, nil
	}
}

func resolveModel(id string, table models.Table) models.Model {
	m, err := models.Resolve(table, id, os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	return m
}

func tuiTrueColor() bool {
	ct := os.Getenv("COLORTERM")
	return ct == "truecolor" || ct == "24bit"
}

func tuiStatusIn(r *root, db store.DB) func(context.Context) tui.StatusIn {
	return func(ctx context.Context) tui.StatusIn {

		eff := r.effort
		if eff == "" {
			eff = r.row.Effort
		}
		b := tui.StatusIn{Model: r.activeID, Effort: eff, Window: r.row.Window, Role: r.role, Approve: r.approve}
		if r.session == nil {
			return b
		}
		b.Session = r.session.ID
		if err := db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(u.prompt), 0), COALESCE(SUM(u.completion), 0), COALESCE(SUM(u.cache_read), 0)
			 FROM usage u JOIN messages m ON m.seq = u.message_seq
			 WHERE m.session_id = ?`, r.session.ID).Scan(&b.Up, &b.Down, &b.CacheRead); err != nil {
			return b
		}
		return b
	}
}

func tuiNews(stateDB, schedDB store.DB, cwd string) func(context.Context) string {
	return func(ctx context.Context) string {
		var last any
		if err := stateDB.QueryRowContext(ctx,
			`SELECT MAX(ended_at) FROM sessions WHERE cwd = ? AND ended_at IS NOT NULL`, cwd).Scan(&last); err != nil {
			return ""
		}
		cutoff := ""
		switch v := last.(type) {
		case time.Time:
			cutoff = v.UTC().Format(time.RFC3339)
		case string:
			cutoff = v
		}
		var name, status, ended string
		if err := schedDB.QueryRowContext(ctx,
			`SELECT j.name, r.status, r.ended_at
			 FROM runs r JOIN jobs j ON j.id = r.job_id
			 WHERE r.ended_at > ? ORDER BY r.seq DESC LIMIT 1`, cutoff).Scan(&name, &status, &ended); err != nil {
			return ""
		}
		at := ended
		if t, err := time.Parse(time.RFC3339, ended); err == nil {
			at = t.UTC().Format("15:04")
		}
		return fmt.Sprintf("· %s %s %s · scheduler runs %s", name, status, at, name)
	}
}

func sessionFor(resumeID string, resume func(id string) (*core.Session, error)) (*core.Session, error) {
	if resumeID == "" {
		return core.NewSession(), nil
	}
	s, err := resume(resumeID)
	if err != nil {
		return nil, err
	}
	if s == nil || s.ID == "" {
		return nil, errors.New("resume: the projection returned no session")
	}
	return s, nil
}

func main() {

	baseURL := flag.String("base-url", "", "OpenAI-compatible endpoint base URL (the worker swap); precedence: flag > RIG_BASE_URL > settings.json baseUrl > the embedded default")
	model := flag.String("model", "", "model name; precedence: flag > RIG_MODEL > settings.json model > the embedded default")
	system := flag.String("system", "", "system prompt; precedence: flag > RIG_SYSTEM > settings.json system > the embedded default")
	allow := flag.String("allow", "", "comma-separated allow-list of tool names; precedence: flag > RIG_ALLOW > settings.json allow > the embedded default")
	retries := flag.Int("retries", 0, "repetition bound on identical failing calls (cleared on success); precedence: flag > RIG_RETRIES > settings.json retries > the embedded default")
	prompt := flag.String("p", "", "one-shot: run the single prompt and exit (the scheduler's worker path)")
	resumeID := flag.String("resume", "", "resume the session with this id (the transcript, the file provenance, and the identity rebuild from the state rows)")
	tuiMode := flag.String("tui", "auto", "the terminal frontend: auto (default; when stdout is a terminal), true (force it), false (the piped CLI)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("rig %s\n", Version)
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "run-job" {
		os.Exit(runJob(os.Args[2:]))
	}

	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Exit(serve(os.Args[2:]))
	}

	if err := checkOneShot(*prompt, *resumeID); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(2)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}

	cfgDir, err := rigHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	cfg, err := config.Load(cfgDir, cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}

	passed := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { passed[f.Name] = true })
	envOr := func(key, def string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return def
	}
	baseURLV := envOr("RIG_BASE_URL", cfg.Settings.BaseURL)
	if passed["base-url"] {
		baseURLV = *baseURL
	}
	modelID := envOr("RIG_MODEL", cfg.Settings.Model)
	if passed["model"] {
		modelID = *model
	}
	systemPrompt := envOr("RIG_SYSTEM", cfg.Settings.System)
	if passed["system"] {
		systemPrompt = *system
	}
	allowList := cfg.Settings.Allow
	if v := os.Getenv("RIG_ALLOW"); v != "" {
		allowList = splitCSV(v)
	}
	if passed["allow"] {
		allowList = splitCSV(*allow)
	}
	retriesN := cfg.Settings.Retries
	if v := os.Getenv("RIG_RETRIES"); v != "" {
		if n, aerr := strconv.Atoi(v); aerr == nil {
			retriesN = n
		}
	}
	if passed["retries"] {
		retriesN = *retries
	}

	row := resolveModel(modelID, cfg.Models)

	py := pythontool.New()
	if python := envOr("RIG_PYTHON", cfg.Settings.Python); python != "" {
		py = pythontool.NewWith(python, pythontool.DefaultHost())
	}
	defer py.Close()
	fmt.Fprintf(os.Stderr, "rig: python kernel host: %s\n", py.Host())

	webSearch := webtool.NewSearch(webtool.SearchConfig{BaseURL: envOr("RIG_SEARXNG_URL", cfg.Settings.SearXNG)})
	proxy := ""
	if cfg.Settings.WebFetchProxy != nil {
		proxy = *cfg.Settings.WebFetchProxy
	}
	if v, ok := os.LookupEnv("RIG_WEB_FETCH_PROXY"); ok {
		proxy = v
	}
	traf := cfg.Settings.Trafilatura
	if v, ok := os.LookupEnv("RIG_TRAFILATURA"); ok {
		traf = &v
	}
	webFetch := webtool.NewFetch(webtool.FetchConfig{Proxy: proxy, Trafilatura: traf})

	pluginsDir := filepath.Join(cfgDir, "plugins")
	if err := os.MkdirAll(filepath.Join(pluginsDir, "pending"), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "rig: plugins: create the pending zone: %v\n", err)
	}
	pluginFiles, err := plugins.List(cfgDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	pluginReports := make([]plugins.Report, 0)
	if len(pluginFiles) > 0 {
		pluginReports, err = plugins.Discover(context.Background(), py, pluginFiles)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rig:", err)
			os.Exit(1)
		}
	}
	native := make(map[string]bool, len(nativeToolNames))
	for _, name := range nativeToolNames {
		native[name] = true
	}
	pluginTools := make([]core.Tool, 0, len(pluginReports))
	pluginInfos := make([]command.PluginInfo, 0, len(pluginReports))
	enabled := cfg.Settings.Plugins.Enabled
	enabledSet := make(map[string]bool, len(enabled))
	for _, n := range enabled {
		enabledSet[n] = true
	}
	enabledN := 0
	for _, rep := range pluginReports {
		info := command.PluginInfo{
			Name: rep.Name, Description: rep.Description, File: rep.File,
			Skipped: rep.Skipped, Reason: rep.Reason,
		}
		if !rep.Skipped && (len(enabledSet) == 0 || enabledSet[rep.Name]) {
			// the enablement (SPEC_GROWTH 9): an enabled plugin wires; a
			// cap (max) keeps only the top Max in file order (the door's enum).
			if cfg.Settings.Plugins.Max > 0 && enabledN >= cfg.Settings.Plugins.Max {
				info.Skipped = true
				info.Reason = "disabled: over the settings.json plugins.max cap"
			} else {
				enabledN++
			}
		} else if !rep.Skipped {
			info.Skipped = true
			info.Reason = "disabled: not in settings.json plugins.enabled"
		}
		pluginInfos = append(pluginInfos, info)
		if info.Skipped {
			// the discovery's loud skips, and the enablement's (SPEC_GROWTH
			// 9): a broken file and a disabled one are both named, one line.
			fmt.Fprintf(os.Stderr, "rig: plugins: %s: %s\n", filepath.Base(rep.File), info.Reason)
			continue
		}
		pluginTools = append(pluginTools, plugins.New(rep.Name, rep.Description, rep.File, rep.Schema, py))
	}

	if err := plugins.Check(pluginReports, native); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}

	sessionsPath := state.StorePath(cfgDir, cwd)
	if err := os.MkdirAll(filepath.Dir(sessionsPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	sdb, quarantined, err := store.Open(sessionsPath, state.Statements(), state.SchemaVersion)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig: state store:", err)
		os.Exit(1)
	}
	if quarantined != "" {
		fmt.Fprintf(os.Stderr, "rig: quarantined corrupt state file: %s\n", quarantined)
	}
	defer sdb.DB.Close()

	todoPath := todostore.StorePath(cfgDir, cwd)
	if err := os.MkdirAll(filepath.Dir(todoPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	tdb, todoQuarantined, todoErr := store.Open(todoPath, todostore.Statements(), todostore.SchemaVersion)
	if todoErr != nil {
		fmt.Fprintln(os.Stderr, "rig: todo store:", todoErr)
		os.Exit(1)
	}
	if todoQuarantined != "" {
		fmt.Fprintf(os.Stderr, "rig: quarantined corrupt todo file: %s\n", todoQuarantined)
	}
	defer tdb.DB.Close()

	remPath := remstore.FilePath(cfgDir)
	if err := os.MkdirAll(filepath.Dir(remPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	rdb, remQuarantined, remErr := store.Open(remPath, remstore.Statements(), remstore.SchemaVersion)
	if remErr != nil {
		fmt.Fprintln(os.Stderr, "rig: rem store:", remErr)
		os.Exit(1)
	}
	if remQuarantined != "" {
		fmt.Fprintf(os.Stderr, "rig: quarantined corrupt rem file: %s\n", remQuarantined)
	}
	defer rdb.DB.Close()

	schedHome := filepath.Join(cfgDir, "scheduler")
	if err := os.MkdirAll(schedHome, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	sgdb, sgQuarantined, sgErr := store.Open(sched.StorePathFor(schedHome, sched.JobKey{Scope: "global"}), sched.Statements(), sched.SchemaVersion)
	if sgErr != nil {
		fmt.Fprintln(os.Stderr, "rig: scheduler store:", sgErr)
		os.Exit(1)
	}
	if sgQuarantined != "" {
		fmt.Fprintf(os.Stderr, "rig: quarantined corrupt scheduler global file: %s\n", sgQuarantined)
	}
	defer sgdb.DB.Close()
	scdb, scQuarantined, scErr := store.Open(sched.StorePathFor(schedHome, sched.JobKey{Scope: "cwd", Hash: sched.CwdHash(cwd)}), sched.Statements(), sched.SchemaVersion)
	if scErr != nil {
		fmt.Fprintln(os.Stderr, "rig: scheduler store:", scErr)
		os.Exit(1)
	}
	if scQuarantined != "" {
		fmt.Fprintf(os.Stderr, "rig: quarantined corrupt scheduler workspace file: %s\n", scQuarantined)
	}
	defer scdb.DB.Close()

	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}

	r := &root{
		baseURL:    baseURLV,
		system:     systemPrompt,
		agents:     cfg.Agents,
		allow:      allowList,
		retries:    retriesN,
		sdb:        sdb,
		remDB:      rdb,
		cwd:        cwd,
		pluginsDir: pluginsDir,
		activeID:   modelID,
		row:        row,
		runtime:    runtimeTable(cfg.Models, modelID, row),

		approve:        firstNonEmpty(cfg.Settings.Approve, approve.Auto),
		approveDefault: firstNonEmpty(cfg.Settings.Approve, approve.Auto),
		tools: map[string]core.Tool{
			"bash": bash.New(), "read": file.Read(), "write": file.Write(), "edit": file.Edit(),
			"ls": fs.LS(), "find": fs.Find(), "grep": fs.Grep(),
			"todo": todoapi.New(tdb), "rem": remapi.New(rdb),

			"scheduler": schedapi.New(sched.Stores{Global: sgdb, Cwd: scdb}, sched.RealCrontab(""), self+" run-job", cfg.Settings.DefaultJobModel),
			"python":    py, "web_search": webSearch, "web_fetch": webFetch,

			"diff": diff.New(sdb),
		},
		pluginTools: pluginTools,
		py:          py,
		pluginsHome: cfgDir,
		pluginInfos: pluginInfos,
	}

	for _, t := range pluginTools {
		r.tools[t.Name()] = t
	}

	r.natives = make(map[string]bool, len(nativeToolNames))
	for _, name := range nativeToolNames {
		r.natives[name] = true
	}
	r.tools["plugins_reload"] = plugins.NewReload(cfgDir, r.natives, py, r.swapPlugins)

	env := &command.Env{
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
		Approve:       func() string { return r.approve },
		SetApprove:    r.switchApprove,
		Tools:         r.tools,
		Plugins:       func() []command.PluginInfo { return r.pluginInfos },
		Reload:        r.reloadPlugins,
		PluginsDir:    pluginsDir,
		SetPlugins:    r.setPluginEnabled,
	}

	var fe core.Frontend
	if *prompt != "" {
		if err := oneshot.ErrPrompt(*prompt); err != nil {
			fmt.Fprintln(os.Stderr, "rig:", err)
			os.Exit(1)
		}
		fe = &oneshot.OneShot{Prompt: *prompt, Out: os.Stdout}
	} else if *tuiMode == "true" || (*tuiMode == "auto" && tui.IsTerminal(os.Stdout.Fd())) {

		th, terr := tui.ResolveTheme(cfg.Settings.Theme, cfg.Theme, tuiTrueColor())
		if terr != nil {
			fmt.Fprintln(os.Stderr, "rig:", terr)
			os.Exit(1)
		}
		fe = tui.New(os.Stdin, os.Stdout,
			tui.WithTheme(th),
			tui.WithStatus(tuiStatusIn(r, sdb)),
			tui.WithNews(tuiNews(sdb, scdb, cwd)),
			tui.WithCommands(command.All(), env),
		)

		if c, ok := fe.(interface{ Close() }); ok {
			defer c.Close()
		}
	} else {
		fe = cli.New(os.Stdin, os.Stdout, cli.WithCommands(command.All(), env))
	}

	session, err := sessionFor(*resumeID, func(id string) (*core.Session, error) {
		return state.Resume(context.Background(), sdb, id)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	r.session = session
	r.fe = fe

	if a, ok := fe.(interface {
		Ask(ctx context.Context, prompt string) bool
	}); ok {
		r.askDoor = a.Ask
	}
	rec := state.NewRecorder(fe, sdb, cwd, modelID, Version, session.ID, session)
	r.rec = rec

	k := wire(r)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runErr := loop.Run(ctx, k)
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "rig:", runErr)
	}

	oneShot, oneShotOK := fe.(*oneshot.OneShot)
	faulted := oneShotOK && oneShot.Faulted()

	switch {
	case runErr != nil && ctx.Err() != nil:
		if e := rec.Close("cancelled"); e != nil {
			fmt.Fprintf(os.Stderr, "rig: session closure: %v\n", e)
		}
	case runErr != nil:
		if e := rec.Close("fault"); e != nil {
			fmt.Fprintf(os.Stderr, "rig: session closure: %v\n", e)
		}
	case faulted:
		if e := rec.Close("fault"); e != nil {
			fmt.Fprintf(os.Stderr, "rig: session closure: %v\n", e)
		}
	default:
		if e := rec.Close("ok"); e != nil {
			fmt.Fprintf(os.Stderr, "rig: session closure: %v\n", e)
		}
	}

	if runErr != nil || faulted {
		os.Exit(1)
	}
}

func runJob(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "rig: usage: run-job <key>")
		return 2
	}
	cfgDir, err := rigHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	cfg, err := config.Load(cfgDir, cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	swapURL := cfg.Settings.SwapURL
	if v := os.Getenv("RIG_SWAP_URL"); v != "" {
		swapURL = v
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	home := filepath.Join(cfgDir, "scheduler")
	if err := os.MkdirAll(home, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	if err := sched.RunJob(args[0], sched.RunOpts{
		Home:      home,
		Crontab:   sched.RealCrontab(""),
		Fetch:     sched.RealFetch(0),
		Spawn:     sched.RealSpawn,
		WorkerCmd: []string{self},
		SwapURL:   swapURL,

		Sandbox:      cfg.Settings.Sandbox,
		SandboxBinds: cfg.Settings.SandboxBinds,
		RigHome:      cfgDir,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	return 0
}

func splitCSV(csv string) []string {
	var out []string
	for _, part := range strings.Split(csv, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
