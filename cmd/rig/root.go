package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/evt"
	"github.com/mrsirg97-rgb/rig/v2/store/graph"
	"io"
	"os"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2"
	"github.com/mrsirg97-rgb/rig/v2/command"
	"github.com/mrsirg97-rgb/rig/v2/config"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/imagemarker"
	"github.com/mrsirg97-rgb/rig/v2/middleware/approve"
	"github.com/mrsirg97-rgb/rig/v2/middleware/toolset"
	"github.com/mrsirg97-rgb/rig/v2/models"
	"github.com/mrsirg97-rgb/rig/v2/plugins"
	"github.com/mrsirg97-rgb/rig/v2/policy/compact"
	effort "github.com/mrsirg97-rgb/rig/v2/policy/effort"
	"github.com/mrsirg97-rgb/rig/v2/policy/empty"
	"github.com/mrsirg97-rgb/rig/v2/provider/openai"
	"github.com/mrsirg97-rgb/rig/v2/store"
	remdom "github.com/mrsirg97-rgb/rig/v2/store/rem/domain"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v2/store/state"
	"github.com/mrsirg97-rgb/rig/v2/swarm"
	viewtool "github.com/mrsirg97-rgb/rig/v2/tool/view"
)

type root struct {
	pluginMax int
	baseURL   string
	system    string
	agents    string
	project   string
	allow     []string
	retries   int
	rounds    int
	resultCap int

	middleware []core.ToolMiddleware
	graph      *graph.Queue

	fe     core.Frontend
	errOut io.Writer
	sdb    store.DB
	remDB  store.DB
	cwd    string
	home   string

	drec decision.Recorder

	proposals  decision.Proposer
	decQ       *decision.Queue
	decRev     *decision.Reviewer
	packScorer *decision.PackScorer
	decide     *decision.Decide
	delegate   func(sched.DelegateInput) (sched.DelegateResult, error)
	eco        *plugins.Ecosystem

	pluginsDir string
	rigHome    string

	activeID string
	row      models.Model
	runtime  models.Table

	effort string
	role   string

	theme          string
	themeDoc       json.RawMessage
	themeTrueColor bool

	approve        string
	approveDefault string
	askDoor        func(ctx context.Context, prompt string) bool

	session *core.Session
	rec     *state.Recorder
	tools   map[string]core.Tool

	swarm    *swarm.Controller
	swarmWhy string
	engine   evt.Engine
	room     broadcast.Room

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

const defaultResultCap = 64 * 1024

func wire(r *root) *rig.Kernel {
	r.applyVision()
	if r.askDoor == nil {
		r.approve = approve.Auto
		r.approveDefault = approve.Auto
	}

	if r.live == nil {
		r.live = toolset.New()
		if r.tools["plugin"] == nil {
			var redo func(ctx context.Context) error
			if r.pluginsHome != "" {
				redo = r.redoPlugins
			}
			r.tools["plugin"] = plugins.NewDoor(r.live, redo, r.eco)
		}
		r.live.Swap(append(r.tableTools(), r.pluginTools...), r.pluginNames()...)
	}
	if r.natives == nil {
		r.natives = make(map[string]bool)
		for _, name := range effectiveNativeNames() {
			r.natives[name] = true
		}
	}
	if r.decide != nil {
		r.natives["decide"] = true
	}
	mw := r.middleware
	if mw == nil {
		mw = r.canonicalMiddleware()
	}

	project, err := config.ProjectAgents(r.rigHome, r.cwd)
	if err != nil {
		panic("rig: wire: " + err.Error())
	}
	r.project = project
	r.fullSystem = r.buildSystem()
	provider, pol := r.buildPair()
	fe := r.frontend()
	k := rig.New(
		rig.WithProvider(provider),
		rig.WithFrontend(fe),
		rig.WithPolicy(pol),
		rig.WithTools(append(
			r.tableTools(),
			r.pluginTools...,
		)...),
		rig.WithMiddleware(mw...),
		rig.WithConcurrent(func(c core.ToolCall) bool { return concurrentNatives[c.Name] }),
		rig.WithEngine(r.engine),
	)
	k.Session = r.session
	r.k = k
	return k
}

func (r *root) buildSystem() string {
	mw := r.middleware
	if mw == nil {
		mw = r.canonicalMiddleware()
	}
	parts := make([]string, 0, 6)
	if r.system != "" {
		parts = append(parts, r.system)
	}
	if seg := sessionSection(r.cwd, r.home); seg != "" {
		parts = append(parts, seg)
	}
	if seg := command.RoleProse(r.role); seg != "" {
		parts = append(parts, seg)
	}
	if r.agents != "" {
		parts = append(parts, r.agents)
	}
	if r.project != "" {
		parts = append(parts, r.project)
	}
	if g := guidelinesOf(mw); g != "" {
		parts = append(parts, g)
	}
	return strings.Join(parts, "\n\n")
}

func sessionSection(cwd, home string) string {
	if cwd == "" {
		return ""
	}
	section := fmt.Sprintf("The session's workspace is %s.", cwd)
	if home != "" {
		section = fmt.Sprintf("The session's workspace is %s and the rig home is %s. A leading ~ in a tool path expands to the rig home.", cwd, home)
	}
	return section
}

func remRow(m remdom.Memory) command.RemRow {
	var src string
	if m.Source != nil {
		src = *m.Source
	}
	return command.RemRow{ID: m.Id, Kind: m.Kind, ScopeLabel: m.ScopeLabel, CreatedAt: m.CreatedAt, Strength: m.Strength, Importance: m.Importance, Source: src, Superseded: m.SupersededBy, Content: m.Content}
}

func (r *root) tableTools() []core.Tool {
	out := r.nativeTools()
	if r.decide != nil {
		out = append(out, r.decide)
	}
	if verdict, ok := r.tools["verdict"]; ok {
		out = append(out, verdict)
	}
	return out
}

func (r *root) nativeTools() []core.Tool {
	names := registeredNativeNames(r.row.Vision)
	out := make([]core.Tool, 0, len(names))
	for _, name := range names {
		tool, ok := r.tools[name]
		if !ok {
			continue
		}
		out = append(out, tool)
	}
	return out
}

func (r *root) buildPair() (core.Provider, core.ContextPolicy) {
	inner := r.buildProvider()
	pol, err := compact.New(inner, r.rec, r.session, r.fullSystem, r.row)
	if err != nil {
		panic("rig: wire: " + err.Error())
	}
	r.compactFn = pol.Compact

	effInner := effort.Decorator(inner, r.effortForWire)

	return toolset.Carry(r.live, compact.Decorator(empty.Decorator(effInner), pol)), pol
}

func (r *root) buildProvider() core.Provider {
	headerTimeout := openai.HeaderTimeoutOff
	if r.row.Remote {
		headerTimeout = 0
	}
	if !r.row.Remote && r.row.APIKey == "" {
		cfg := openai.Config{
			BaseURL:       r.baseURL,
			Model:         r.activeID,
			HeaderTimeout: headerTimeout,
		}
		if r.row.Vision {
			cfg.BlobsDir = r.blobsDir()
		}
		return openai.NewWithConfig(cfg)
	}
	baseURL := r.baseURL
	if r.row.Remote {
		baseURL = r.row.BaseURL
	}
	return openai.NewWithConfig(openai.Config{
		BaseURL:       baseURL,
		Model:         r.activeID,
		APIKey:        r.row.APIKey,
		Remote:        r.row.Remote,
		Reasoning:     r.row.Reasoning,
		ProviderPin:   r.row.ProviderPin,
		CacheControl:  r.row.CacheControl,
		Retries:       r.row.Retries,
		BlobsDir:      r.blobsDir(),
		HeaderTimeout: headerTimeout,
	})
}

func (r *root) applyVision() {
	if r.row.Vision {
		if r.tools == nil {
			r.tools = map[string]core.Tool{}
		}
		if _, ok := r.tools["view"]; !ok {
			r.tools["view"] = viewtool.New(r.blobsDir())
		}
		return
	}
	delete(r.tools, "view")
}

func (r *root) blobsDir() string {
	if r.rigHome == "" {
		if h, err := rigHome(); err == nil {
			r.rigHome = h
		}
	}
	return imagemarker.BlobsDir(r.rigHome)
}

func (r *root) frontend() core.Frontend {
	if r.decRev == nil {
		return r.rec
	}
	return decision.TurnEnds(r.rec, r.decRev)
}

func (r *root) swapIn(s *core.Session, rec2 *state.Recorder) {
	r.rec.Retarget(s.ID, s)
	r.rec = rec2
	r.session = s
	r.k.Frontend = r.frontend()
	r.k.Session = s

	project, err := config.ProjectAgents(r.rigHome, r.cwd)
	if err != nil {
		panic("rig: wire: " + err.Error())
	}
	r.project = project
	r.fullSystem = r.buildSystem()
	provider, pol := r.buildPair()
	r.k.Provider = provider
	r.k.Policy = pol
}

func newFleet() (evt.Engine, broadcast.Room) {
	engine := evt.NewEngine()
	return engine, broadcast.NewRoom("fleet", func(origin int64) broadcast.Transport {
		return broadcast.NewLoopTransport(origin, engine, rig.PriorityFleet)
	})
}

func (r *root) listen() {
	r.room.Add(rig.MemberFrontend).Subscribe(context.Background(), func(err error, messages ...broadcast.Message) {
		if err != nil {
			return
		}
		for _, m := range messages {
			if m.Event() == nil {
				continue
			}
			r.deliver(m.Event())
		}
	})
}

func (r *root) deliver(ev core.Event) {
	defer func() {
		if p := recover(); p != nil {
			out := r.errOut
			if out == nil {
				out = os.Stderr
			}
			fmt.Fprintf(out, "rig: fleet: frontend: recovered from panic: %v\n", p)
		}
	}()
	switch ev.(type) {
	case core.Notice, core.SwarmStatus, core.Phase:
	default:
		return
	}
	if fe := r.rec; fe != nil {
		fe.Notify(ev)
		return
	}
	if n, ok := ev.(core.Notice); ok {
		out := r.errOut
		if out == nil {
			out = os.Stderr
		}
		fmt.Fprintln(out, "rig: "+n.Source+": "+n.Text)
	}
}
