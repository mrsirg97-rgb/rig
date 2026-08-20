// Command rig is the composition root: every dependency explicit in one
// call, wired once at startup. Flags, env, and config files (SPEC_CONFIG):
// the knobs are a four-layer resolution — flags > env > file > embedded
// defaults — and the models table lives in the embedded config file.
package main

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"path/filepath"

	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
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
	"github.com/mrsirg97-rgb/rig/middleware/guard"
	"github.com/mrsirg97-rgb/rig/middleware/perm"
	"github.com/mrsirg97-rgb/rig/middleware/toolset"
	"github.com/mrsirg97-rgb/rig/models"
	"github.com/mrsirg97-rgb/rig/plugins"
	"github.com/mrsirg97-rgb/rig/policy/compact"
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

// Version is the binary's release version: pre-1.0, still feature-
// complete (the home and the plugins in 0.4.0, the plugin provenance
// rule in 0.5.0, the worker jail in 0.6.0, the plugin reload and the
// forge in 0.7.0) — the 1.0 tag waits for lived use.
const Version = "0.7.0"

// root is the process's mutable wiring state (SPEC_COMMANDS 2): the
// active model, the row, the recorder, the session — the state the
// command's closures read and rewrite at call time, so a swap (new, a
// resume, a model switch) is visible to every closure with no re-wiring.
// The closures are the root's; the command package sees core and models
// and nothing else.
type root struct {
	baseURL string
	system  string // the resolved system prompt (the chain's result)
	agents  string // the assembled AGENTS.md pair (SPEC_CONFIG 6)
	allow   []string
	retries int

	// middleware is the root's chain; nil = the default [perm, guard]
	// (the tests set it to name a guideline participant, SPEC_CONFIG 6).
	middleware []core.ToolMiddleware

	fe    core.Frontend // the raw frontend (cli or oneshot) — the recorder wraps it
	sdb   store.DB      // the state store
	remDB store.DB      // the rem store (the AutoReflect seam)
	cwd   string

	// pluginsDir is the rig home's plugins/ directory (SPEC_SANDBOX 2):
	// the perm's provenance rule guards it, and the /plugins verbs work
	// on it (the pending zone is pluginsDir/pending).
	pluginsDir string

	activeID string       // the active model id — the root's one mutable string every closure reads
	row      models.Model // the active row (the root's own resolution)
	runtime  models.Table // the merged table plus, when the resolution overlaid or synthesized the active row, that row (6)

	session *core.Session
	rec     *state.Recorder
	tools   map[string]core.Tool // the same live instances the kernel executes

	// pluginTools is the loaded plugins, in file order — the wire's tail
	// after the natives (SPEC_PLUGINS 2): absent when there are none.
	pluginTools []core.Tool

	// live is the root's live tool list (SPEC_PLUGINS 8): the wire's
	// truth — the natives (the set's order) and the loaded plugins (the
	// file order), swapped atomically by a reload. The loop's snapshot
	// is the bootstrap; the table is what the next turn's request
	// stamps and the exec resolves (the router, the carrier).
	live *toolset.Table
	// natives is the native set (SPEC_PLUGINS 2, 8): the collision
	// check's refusal set, including plugins_reload itself.
	natives map[string]bool
	// py is the shared python kernel (SPEC_PLUGINS 1, 3, 8): the
	// discovery's and the plugin calls' ride — one process, one session,
	// the namespace shared (the forge's import is the python tool's).
	py plugins.Kernel
	// pluginsHome is the rig home (SPEC_PLUGINS 6, 8): the listing is
	// its top-level *.py (the pending zone is its, SPEC_SANDBOX 2).
	pluginsHome string
	// pluginInfos is the listing's fact (SPEC_PLUGINS 4, 8): the loaded
	// and the skipped, in file order — the command's listing reads it
	// at call time (the Env's closure), so a reload's swap is visible
	// with no re-wiring.
	pluginInfos []command.PluginInfo

	fullSystem string // system + the middleware guidelines (computed in wire)
	k          *rig.Kernel
	// the forced seam (3): the current policy's Compact, as a method value —
	// set on every pair build, so a rebuilt pair carries its own seam.
	compactFn func(ctx context.Context) (core.Compacted, bool, error)
}

// wire assembles the kernel's dependencies. Swapping a seam is a change
// here and nowhere else. Deliverable 8 (SPEC_COMPACT): the first
// non-passthrough policy — the compact policy wraps the shared inner
// instance, the overflow decorator wraps the same inner, and the root
// wires the AutoReflect seam (decision 6) from the rem store it owns.
// Deliverable 9 (SPEC_COMMANDS): the mutable state is named once (root),
// the pair rebuild is the one function the model switch and /new share,
// and the command's closures read it at call time.
func wire(r *root) *rig.Kernel {
	// The live tool list (SPEC_PLUGINS 8): the root's one truth for the
	// wire and the exec — the natives (the set's order) and the loaded
	// plugins (the file order). A reload's swap is one atomic write,
	// effective on the next turn (never mid-turn), zero loop lines —
	// the models-switch's semantics (SPEC_COMMANDS 6) on the kernel's
	// tool list. The loop's snapshot below is the bootstrap (the chain's
	// lookup, the test chains' exec); the table is what counts.
	if r.live == nil {
		r.live = toolset.New(append(r.nativeTools(), r.pluginTools...)...)
	}
	if r.natives == nil {
		r.natives = make(map[string]bool, len(nativeToolNames))
		for _, name := range nativeToolNames {
			r.natives[name] = true
		}
	}
	mw := r.middleware
	if mw == nil {
		// the root's chain is [router, perm, guard]: the router (the
		// table's exec end) lists first — innermost, first-listed is
		// innermost — so the chain's participants still bound what the
		// table serves (SPEC_PLUGINS 8's seam). The provenance rule
		// lists before the allow-list, as before (SPEC_SANDBOX 2).
		mw = []core.ToolMiddleware{
			toolset.Resolve(r.live),
			perm.Plugins(r.pluginsDir),
			perm.Allowlist(r.allow...),
			guard.Bound(r.retries),
		}
	}
	// The guidelines ride the system prompt, not the chain (decision 6):
	// prompt assembly belongs to the prompt, and the prompt string is the
	// root's — zero loop change. The order (SPEC_CONFIG 6, SPEC_UX 2) is
	// descending proximity: the operator's identity prompt, then the
	// user's project contract, then the remembered notes (the cwd's
	// recent memories — the session-start recall), then the participants'
	// operational prose. Empty segments are skipped, so an absent pair
	// is 0.2.0's bytes exactly.
	r.fullSystem = r.buildSystem()
	provider, pol := r.buildPair()
	k := rig.New(
		rig.WithProvider(provider),
		rig.WithFrontend(r.rec),
		rig.WithPolicy(pol),
		rig.WithTools(append(
			r.nativeTools(),  // the set's order (SPEC_PLUGINS 2, 8)
			r.pluginTools..., // the loaded plugins, in file order (SPEC_PLUGINS 2)
		)...),
		rig.WithMiddleware(mw...),
	)
	k.Session = r.session // one identity: the loop's session is the transcript's
	r.k = k
	return k
}

// buildSystem is the prompt assembly (SPEC_CONFIG 6, SPEC_UX 2): the
// system, the AGENTS.md pair, the remembered notes, and the
// participants' guidelines — in that order, empty segments skipped.
// It is computed at session start and the refresh points (new, resume),
// never per turn: the segment rides the prefix, and its cost is prefix
// tokens — the cap is the point.
func (r *root) buildSystem() string {
	mw := r.middleware
	if mw == nil {
		mw = []core.ToolMiddleware{
			perm.Plugins(r.pluginsDir),
			perm.Allowlist(r.allow...),
			guard.Bound(r.retries),
		}
	}
	parts := make([]string, 0, 4)
	if r.system != "" {
		parts = append(parts, r.system)
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

// rememberedK is the session-start recall budget (SPEC_UX 2): the cap
// keeps the memory cheaper than what it saves.
const rememberedK = 8

// remembered is the cwd's recent notes (SPEC_UX 2): absent notes are an
// absent segment (today's bytes exactly). A store read at session start
// and the refresh points — never per turn.
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

// renderRemembered is the segment (SPEC_UX 2): the header, one line
// per note (newest first, whitespace collapsed to the line), capped at
// 1500 characters total — over the cap the oldest notes trim first, and
// a single note over the cap alone is cut with the loud ellipsis.
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
	// the newest note alone overflows: cut it to the cap, loud.
	budget := capChars - chars(header) - 1 - 2 - 1 // the newline, the bullet, the ellipsis
	line := []rune(strings.TrimPrefix(lines[0], "- "))
	if len(line) > budget {
		line = line[:budget]
	}
	return header + "\n- " + string(line) + "…"
}

// nativeTools is the wire's head (SPEC_PLUGINS 2, 8): the natives in
// the set's order — the table's head, and the loop's snapshot's.
func (r *root) nativeTools() []core.Tool {
	out := make([]core.Tool, 0, len(nativeToolNames))
	for _, name := range nativeToolNames {
		out = append(out, r.tools[name])
	}
	return out
}

// buildPair is the provider+policy pair rebuild (SPEC_COMMANDS 4, 6):
// the fresh inner on the active id, the fresh compact policy over the
// current recorder, session, and row, and the overflow decorator over
// the same inner. The loop reads k.Provider / k.Policy fresh at each
// turn start, so the rebuilt pair takes effect on the next turn's
// request by construction — the switch is next-turn, not by guard.
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
		panic("rig: wire: " + err.Error()) // a violating row is refused at construction
	}
	r.compactFn = pol.Compact // the rebuilt pair carries its own forced seam
	// the request's end of the live tool list (SPEC_PLUGINS 8): the
	// table's specs stamped into every request, per call — a reload's
	// swap rides the next turn's request by construction (the models-
	// switch's semantics, the loop untouched).
	return toolset.Carry(r.live, compact.Decorator(inner, pol)), pol
}

// swapIn is the handoff's tail (SPEC_COMMANDS 4): the retiring recorder
// is re-pointed first — its in-flight Input lands the next user row
// (and the files snapshot) under the new session, then retires — and
// then the kernel's frontend and session are the new ones and the pair
// is rebuilt on (recorder, session, the active row). Same goroutine, no
// locks: the loop reads k.Session after Input returns, so the prompt it
// appends is already on the new session.
func (r *root) swapIn(s *core.Session, rec2 *state.Recorder) {
	r.rec.Retarget(s.ID, s)
	r.rec = rec2
	r.session = s
	r.k.Frontend = rec2
	r.k.Session = s
	// the refresh points (SPEC_UX 2): a new or resumed session re-reads
	// the cwd's notes — recall at session start, never per turn.
	r.fullSystem = r.buildSystem()
	provider, pol := r.buildPair()
	r.k.Provider = provider
	r.k.Policy = pol
}

// compactNow is the compact command's root closure (SPEC_COMMANDS 3):
// run the policy's forced seam over the same internal action the trigger
// path runs; on success deliver the event to the current recorder — the
// recorder lands the summary row plus its usage row, re-lands the kept
// tail, and forwards to the CLI. The ⧉ line is the command's output,
// exactly once (the command prints no second line).
func (r *root) compactNow(ctx context.Context) (core.Compacted, bool, error) {
	ev, compacted, err := r.compactFn(ctx)
	if err != nil || !compacted {
		return ev, compacted, err
	}
	r.rec.Notify(ev)
	return ev, true, nil
}

// newSession is the new command's root closure (SPEC_COMMANDS 4): close
// the current row ok, mint the fresh session and recorder, Ensure the
// new row before any row lands under it, and swap. A refused close is
// loud and the swap does not happen: the current session continues.
func (r *root) newSession(ctx context.Context) (string, error) {
	if err := r.rec.Close("ok"); err != nil {
		return "", fmt.Errorf("new: %v", err)
	}
	s2 := core.NewSession()
	rec2 := state.NewRecorder(r.fe, r.sdb, r.cwd, r.activeID, Version, s2.ID, s2)
	if err := rec2.Ensure(); err != nil {
		return "", fmt.Errorf("new: %v", err)
	}
	r.swapIn(s2, rec2)
	return s2.ID, nil
}

// sessionList is the sessions command's list read (SPEC_COMMANDS 5): the
// store's rows (newest first, capped) with the live session marked.
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

// sessionShow is the sessions show projection (SPEC_COMMANDS 5): the same
// state.Resume function -resume uses — one projection, one truth —
// rendered plain by the command package.
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

// sessionResume is the sessions resume root closure (SPEC_COMMANDS 5):
// validate before mutate — the projection exists (the unknown id is loud
// here, before the current row is touched) — and then the same handoff
// as new, over the resumed session: the recorder adopts the existing row
// (7's -resume semantics), so the claims and the sources attribute to
// it and its file provenance is the projection's.
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

// switchModel is the models switch root closure (SPEC_COMMANDS 6): the
// row must exist in the runtime table (unknown id names the known), then
// the pair rebuilds on the current recorder and session with the new
// row — the transcript, the session row, and the recorder are untouched:
// the switch is not a new session, and the row keeps the model the
// session started with (a historical record; the switch is not
// retroactive).
func (r *root) switchModel(ctx context.Context, id string) error {
	row, ok := r.runtime.Get(id)
	if !ok {
		return fmt.Errorf("models: no row for %q (known: %s)", id, strings.Join(r.runtime.Known(), ", "))
	}
	r.row = row
	r.activeID = id
	provider, pol := r.buildPair()
	r.k.Provider = provider
	r.k.Policy = pol
	return nil
}

// swapPlugins is the reload's rebuild (SPEC_PLUGINS 8): the discovery's
// reports into the live list — the natives (the set's order) and the
// loaded (the file order), the wire's shape — one atomic write to the
// table, and the listing's fact follows (the command's listing reads
// it at call time). Called only after Check refused nothing, so a
// refusal leaves the table — and the wire — whole.
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
	r.live.Set(tools)
	r.pluginInfos = infos
	return command.RenderPlugins(infos, "reload"), nil
}

// reloadPlugins is the reload's action (SPEC_PLUGINS 8): the listing
// (no files, no kernel), the discovery through the shared kernel (the
// loud skips in the reply), the collision check (the refusal before the
// swap), and the rebuild — the operator's /plugins reload door and the
// approve's tail. The tool's door (plugins_reload) is the same action
// with its discovery and the root's swap.
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

// runtimeTable is the models command's table (SPEC_COMMANDS 6;
// SPEC_CONFIG 4): the merged table, with the active row replaced by the
// resolved row when the resolution overlaid or synthesized it — so
// /models shows what is in effect, and models <id> can switch back to
// it. A table the operator cannot see is a table the operator cannot
// use.
func runtimeTable(t models.Table, active string, resolved models.Model) models.Table {
	if base, ok := t.Get(active); ok && base == resolved {
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

// guidelinesOf collects the system-prompt prose of the participants that
// contribute it (SPEC_HARDENING decision 6): assertion-checked, in listed
// order, joined with a blank line. Wrap-only participants contribute
// nothing.
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

// ErrResumeWithPrompt is the root's construction refusal: one-shot stays
// one-shot — the worker's stdout is the answer, a resumed transcript is
// the REPL's.
var ErrResumeWithPrompt = errors.New("rig: -resume is not available with -p (one-shot stays one-shot)")

// checkOneShot is the -p/-resume construction check, loud before any
// store is opened or a seam wired.
func checkOneShot(prompt, resumeID string) error {
	if prompt != "" && resumeID != "" {
		return ErrResumeWithPrompt
	}
	return nil
}

// userHome is $HOME, loud nowhere (the caller's refusal names it).
func userHome() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}

// nativeToolNames is the native tool set (SPEC_PLUGINS 2): the wire's
// head, and the plugin collision check's refusal set. A native tool
// added or retired updates this list and nothing else.
var nativeToolNames = []string{"bash", "read", "write", "edit", "ls", "find", "grep", "todo", "rem", "scheduler", "python", "web_search", "web_fetch", "diff", "plugins_reload"}

// rigHome is the config home (SPEC_CONFIG 11, SPEC_PLUGINS 6): $RIG_HOME
// if set (the operator's spelling, used as-is), else ~/.rig — the
// .pi/.omp convention, not the XDG one. The migration, once and
// deterministic, and a **default-path event**: only when $RIG_HOME is
// unset, if the resolved home is absent and the old ~/.config/rig
// exists, the rename (atomic — both live under $HOME) and the one
// line. Under an explicit override the migration never runs — the
// override is isolation, not a move order (the RIG_HOME=$(mktemp -d)
// shape); a failed rename refuses the start.
func rigHome() (string, error) {
	if v := os.Getenv("RIG_HOME"); v != "" {
		return v, nil // as-is, whatever it holds: the old home stays put
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
				// both homes exist: the present home won and the
				// migration will never fire — name it, so a machine
				// where a dev build half-birthed ~/.rig does not lose
				// its real data to silence (the operator merges by
				// hand; deletion is theirs, never the runtime's).
				fmt.Fprintf(os.Stderr, "rig: the old config home still exists: %s (the home here won: %s; merge or prune it by hand)\n", oldHome, newHome)
			}
		}
		return newHome, nil
	}
}

// resolveModel is the compaction row resolution (SPEC_COMPACT 2, 8;
// SPEC_CONFIG 4), loud before any store is opened: the table row for the
// active id, with the RIG_MODEL_* env overlaid on its fields, else an env
// synthesis from RIG_MODEL_*, else a refusal naming the id, the known
// ids, and the env. A row that violates the invariants is refused at
// start.
func resolveModel(id string, table models.Table) models.Model {
	m, err := models.Resolve(table, id, os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	return m
}

// tuiTrueColor is the theme's color mode (SPEC_TUI 7: named, automatic,
// not configurable): the terminal reports 24-bit, else the downconvert.
func tuiTrueColor() bool {
	ct := os.Getenv("COLORTERM")
	return ct == "truecolor" || ct == "24bit"
}

// tuiStatusIn is the TUI status line's and the startup block's data at
// call time (SPEC_TUI 3, amended): the model, the window, and the
// session's usage totals from the state rows. The used number is the
// frontend's own (the Done and Compacted events); the read here is the
// root's (the store is its), at the refresh points only.
func tuiStatusIn(r *root, db store.DB) func(context.Context) tui.StatusIn {
	return func(ctx context.Context) tui.StatusIn {
		b := tui.StatusIn{Model: r.activeID, Effort: r.row.Effort, Window: r.row.Window}
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

// tuiNews is the TUI news line (SPEC_TUI 4): the latest run of the
// scheduler's jobs for this cwd, when it postdates the last session's
// end here; nothing otherwise. The read is the root's.
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

// sessionFor is the root's session construction: fresh by default,
// resumed by -resume. A resumed session keeps its identity (the recorder
// adopts the existing row — todo's claims and rem's sources attribute to
// it — and the per-process state, the guard's counters and the slot,
// starts fresh, named by SPEC_HARDENING decision 5).
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
	// The flags carry no defaults (SPEC_CONFIG 2, 5): the defaults live in
	// the embedded config file, and the chain — flags > env > file >
	// embedded — resolves each key. A passed flag always wins, whatever
	// its value (flag.Visit reports exactly which were passed); the
	// default endpoint is the worker swap itself, in the embedded file.
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

	// Scheduler verb dispatch: run-job lands before state/session wiring.
	// It is its own lifecycle (own stores, own record) and must not touch
	// the REPL's closure order.
	if len(os.Args) > 1 && os.Args[1] == "run-job" {
		os.Exit(runJob(os.Args[2:]))
	}

	// The -p/-resume conflict is a construction error: it is refused
	// before any store is opened or a seam wired.
	if err := checkOneShot(*prompt, *resumeID); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(2)
	}

	// The config load (SPEC_CONFIG 1): after flag parse, before any store
	// is opened — the same position resolveModel's refusal holds today
	// (loud before the stores). One load function, three entry modes; the
	// run-job path above runs the same load in its own cwd.
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	digest := sha1.Sum([]byte(cwd))
	// The rig home (SPEC_CONFIG 11): $RIG_HOME over ~/.rig, the old
	// home migrated (renamed, one line) when the condition holds.
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

	// The per-key chain (SPEC_CONFIG 2): flag (if passed) > env (if set)
	// > file (if the key is set) > embedded. cfg.Settings is the
	// file-over-embedded resolution Load returned; the env is the 0.2.0
	// empty=unset semantics, except the two presence-aware keys, for which
	// present is set — even empty.
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

	// The compaction row (SPEC_COMPACT 2, 8; SPEC_CONFIG 4): resolved loud
	// before any store is opened — a job whose window minus reserve leaves
	// too little to work with fails at start, not as a worker that compacts
	// every turn and logs false successes.
	row := resolveModel(modelID, cfg.Models)
	// The python kernel: one persistent IPython session for the process
	// (rig runs one session per process); the root owns its teardown.
	// The interpreter is the chain's — RIG_PYTHON over settings.json
	// python over the embedded none (and the seam's contract: no lazy venv
	// bootstrap); the host choice is logged so an installed host and the
	// embedded one cannot drift silently.
	py := pythontool.New()
	if python := envOr("RIG_PYTHON", cfg.Settings.Python); python != "" {
		py = pythontool.NewWith(python, pythontool.DefaultHost())
	}
	defer py.Close()
	fmt.Fprintf(os.Stderr, "rig: python kernel host: %s\n", py.Host())

	// The web tools: web_search and web_fetch, one leaf package. The knobs
	// are the chain's: RIG_SEARXNG_URL over the file's over the embedded is
	// the SearXNG instance (the web-tools compose publishes one on
	// loopback); RIG_WEB_FETCH_PROXY over the file's over the embedded is
	// the egress proxy (set empty = direct, presence-aware at every layer);
	// RIG_TRAFILATURA over the file's over the embedded none is the
	// extraction binary (empty = the stdlib pass carries, presence-aware).
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

	// The python plugins (SPEC_PLUGINS): discovery at startup, through
	// the shared python kernel — the same kernel as the python tool, the
	// namespace shared (the cost is named, SPEC_PLUGINS 3). The listing
	// is filename order (os.ReadDir sorts): the discovery, the skip
	// lines, the first collision refusal, and the /plugins listing all
	// follow it. No plugins directory, or an empty one, is a no-op that
	// never starts the kernel (the fixture runs take the 0.2.0 wire).
	// A broken file is a loud skip (one line, file + field); a name
	// colliding with a native tool refuses loud — before the stores.
	//
	// The pending zone (SPEC_SANDBOX 2) is created with the home: the
	// forge's landing zone is a fact of the runtime, silent and
	// idempotent — the write tool makes no directories, so the model's
	// first pending write must not depend on the operator's mkdir.
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
	for _, rep := range pluginReports {
		pluginInfos = append(pluginInfos, command.PluginInfo{
			Name: rep.Name, Description: rep.Description, File: rep.File,
			Skipped: rep.Skipped, Reason: rep.Reason,
		})
		if rep.Skipped {
			fmt.Fprintf(os.Stderr, "rig: plugins: %s: %s\n", filepath.Base(rep.File), rep.Reason)
			continue
		}
		pluginTools = append(pluginTools, plugins.New(rep.Name, rep.Description, rep.File, rep.Schema, py))
	}
	// The collision refusal (SPEC_PLUGINS 2's rule; the reload's door
	// shares it, 8) — after the loud skips, before the registration.
	if err := plugins.Check(pluginReports, native); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}

	sessionsPath := filepath.Join(cfgDir, "sessions", hex.EncodeToString(digest[:6])+".sqlite")
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

	// The task queue: workspace-keyed, opened once, loud on corruption.
	// (SPEC_STATE's paths, digest prefix twelve.)
	todoPath := filepath.Join(cfgDir, "todo", hex.EncodeToString(digest[:12])+".sqlite")
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

	// The memory store: one hybrid file (global and project scopes) under
	// the user config directory; opened once, loud on corruption.
	remPath := filepath.Join(cfgDir, "rem", "rem.sqlite")
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

	// The scheduler stores: both scopes — global (this user) and this
	// workspace — opened once under the scheduler home, loud on corruption.
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

	// The root (SPEC_COMMANDS 2): the process's mutable wiring state, named
	// once. The command's closures read it at call time, so a swap (new, a
	// resume, a model switch) is visible to every closure with no
	// re-wiring; the closures are the root's, and the command package sees
	// core and models and nothing else.
	r := &root{
		baseURL:    baseURLV,
		system:     systemPrompt,
		agents:     cfg.Agents, // global then project, the load's assembly (6)
		allow:      allowList,
		retries:    retriesN,
		sdb:        sdb,
		remDB:      rdb,
		cwd:        cwd,
		pluginsDir: pluginsDir,
		activeID:   modelID,
		row:        row,
		runtime:    runtimeTable(cfg.Models, modelID, row),
		tools: map[string]core.Tool{
			"bash": bash.New(), "read": file.Read(), "write": file.Write(), "edit": file.Edit(),
			"ls": fs.LS(), "find": fs.Find(), "grep": fs.Grep(),
			"todo": todoapi.New(tdb), "rem": remapi.New(rdb),
			// the scheduler's default job model is the chain's (SPEC_CONFIG 5):
			// the file's defaultJobModel over the embedded, resolved by Load;
			// the tool's description and schema are built from it.
			"scheduler": schedapi.New(sched.Stores{Global: sgdb, Cwd: scdb}, sched.RealCrontab(""), self+" run-job", cfg.Settings.DefaultJobModel),
			"python":    py, "web_search": webSearch, "web_fetch": webFetch,
			// the diff tool (SPEC_DIFF): the state DB the way todo takes
			// its own; the session comes from ctx.
			"diff": diff.New(sdb),
		},
		pluginTools: pluginTools,
		py:          py,
		pluginsHome: cfgDir,
		pluginInfos: pluginInfos,
	}
	// the plugins ride the tools map too (the lookup seam; the wire's
	// order is pluginTools', SPEC_PLUGINS 2).
	for _, t := range pluginTools {
		r.tools[t.Name()] = t
	}
	// The native set (SPEC_PLUGINS 2, 8) and the one new native: the
	// model's reload (SPEC_PLUGINS 8) — the re-discovery and the root's
	// swap, no arguments. The operator's /plugins reload and the
	// approve's tail are the same action (the root's method); the
	// collision check's refusal set includes plugins_reload itself.
	r.natives = make(map[string]bool, len(nativeToolNames))
	for _, name := range nativeToolNames {
		r.natives[name] = true
	}
	r.tools["plugins_reload"] = plugins.NewReload(cfgDir, r.natives, py, r.swapPlugins)

	// The command's env (SPEC_COMMANDS 2): closures, not handles. The
	// Steer seam is the frontend's — the dispatcher fills it in its
	// WithCommands; the env built here carries everything else.
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
		Tools:         r.tools,
		Plugins:       func() []command.PluginInfo { return r.pluginInfos },
		Reload:        r.reloadPlugins,
		PluginsDir:    pluginsDir,
	}

	// The frontend: the REPL by default; one-shot under -p (deliverable 2's
	// seam). The REPL is the only frontend that dispatches (SPEC_COMMANDS
	// 9, 10): the one-shot takes no commands — a command-shaped prompt is
	// a prompt, and nothing is hijacked from it.
	var fe core.Frontend
	if *prompt != "" {
		if err := oneshot.ErrPrompt(*prompt); err != nil {
			fmt.Fprintln(os.Stderr, "rig:", err)
			os.Exit(1)
		}
		fe = &oneshot.OneShot{Prompt: *prompt, Out: os.Stdout}
	} else if *tuiMode == "true" || (*tuiMode == "auto" && tui.IsTerminal(os.Stdout.Fd())) {
		// the terminal frontend (SPEC_TUI): the same REPL semantics
		// (the commands, the seam, the exits) in the themed stream;
		// the theme is the file's document, the TUI owns the schema.
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
		// the TUI owns the terminal's mode; put it back on the way out.
		if c, ok := fe.(interface{ Close() }); ok {
			defer c.Close()
		}
	} else {
		fe = cli.New(os.Stdin, os.Stdout, cli.WithCommands(command.All(), env))
	}

	// The session: fresh by default; -resume rebuilds the transcript, the
	// file provenance, and the identity from the state rows (decision 5).
	session, err := sessionFor(*resumeID, func(id string) (*core.Session, error) {
		return state.Resume(context.Background(), sdb, id)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	r.session = session
	r.fe = fe // the handoff builds the fresh recorder over the same inner frontend
	rec := state.NewRecorder(fe, sdb, cwd, *model, Version, session.ID, session)
	r.rec = rec

	k := wire(r)

	// Interrupt cancels the turn at its next boundary.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runErr := loop.Run(ctx, k)
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "rig:", runErr)
	}

	// One-shot fault propagation: a faulted turn ends the session
	// non-zero. The worker's run record derives status from exit, so a
	// faulted worker that exited ok would log a false success. The REPL
	// keeps its continue-on-fault semantics: its faults never reach this
	// branch.
	oneShot, oneShotOK := fe.(*oneshot.OneShot)
	faulted := oneShotOK && oneShot.Faulted()

	// the session row closes with what the run was
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

// runJob is the scheduler verb's cold-shell path: argv supplies the key,
// RunJob opens its own stores, fires under the busy policy, and records
// the outcome. Recorded outcomes exit 0; loud refusals exit non-zero. The
// swapUrl is the chain's (SPEC_CONFIG 5): RIG_SWAP_URL over the file's
// swapUrl over the embedded — the same load as every entry mode.
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
		// the worker jail (SPEC_SANDBOX 1, 5): the settings' profile
		// and binds (the chain: file over the embedded default — the
		// settings spec's two keys, no env, no flags), and the
		// operator's rig home (the kernel line's source).
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
