package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3"
	"github.com/mrsirg97-rgb/rig/v3/command"
	"github.com/mrsirg97-rgb/rig/v3/config"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/decision"
	"github.com/mrsirg97-rgb/rig/v3/frontend/cli"
	"github.com/mrsirg97-rgb/rig/v3/frontend/oneshot"
	"github.com/mrsirg97-rgb/rig/v3/frontend/tui"
	"github.com/mrsirg97-rgb/rig/v3/frontend/web"
	"github.com/mrsirg97-rgb/rig/v3/loop"
	"github.com/mrsirg97-rgb/rig/v3/middleware/approve"
	"github.com/mrsirg97-rgb/rig/v3/models"
	"github.com/mrsirg97-rgb/rig/v3/plugins"
	machineroom "github.com/mrsirg97-rgb/rig/v3/room"
	"github.com/mrsirg97-rgb/rig/v3/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v3/store/decision"
	"github.com/mrsirg97-rgb/rig/v3/store/graph"
	remstore "github.com/mrsirg97-rgb/rig/v3/store/rem"
	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v3/store/scope"
	"github.com/mrsirg97-rgb/rig/v3/store/state"
	todostore "github.com/mrsirg97-rgb/rig/v3/store/todo"
	"github.com/mrsirg97-rgb/rig/v3/swarm"
	"github.com/mrsirg97-rgb/rig/v3/tool/bash"
	"github.com/mrsirg97-rgb/rig/v3/tool/delegate"
	"github.com/mrsirg97-rgb/rig/v3/tool/file"
	pythontool "github.com/mrsirg97-rgb/rig/v3/tool/python"
	remapi "github.com/mrsirg97-rgb/rig/v3/tool/rem"
	schedapi "github.com/mrsirg97-rgb/rig/v3/tool/scheduler"
	sessionstool "github.com/mrsirg97-rgb/rig/v3/tool/sessions"
	todoapi "github.com/mrsirg97-rgb/rig/v3/tool/todo"
	verdicttool "github.com/mrsirg97-rgb/rig/v3/tool/verdict"
	webtool "github.com/mrsirg97-rgb/rig/v3/tool/web"
)

const Version = "3.3.4"

func claimPriority(prompt string, served bool) (int, error) {
	if v := os.Getenv("RIG_PRIORITY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("RIG_PRIORITY: expected a non-negative integer, got %q", v)
		}
		return n, nil
	}
	if served {
		return machineroom.PriorityOutside, nil
	}
	if prompt != "" {
		return machineroom.PriorityFleet, nil
	}
	return machineroom.PriorityTurn, nil
}

func openStore(label, path string, statements []string, version int, migrate ...func(*sql.Tx, int, int) (string, error)) (store.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return store.DB{}, err
	}
	db, quarantined, report, err := store.Open(path, statements, version, migrate...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rig: %s store: %v\n", label, err)
		return store.DB{}, err
	}
	if quarantined != "" {
		fmt.Fprintf(os.Stderr, "rig: quarantined corrupt %s file: %s\n", label, quarantined)
	}
	if report != "" {
		fmt.Fprintf(os.Stderr, "rig: %s\n", report)
	}
	return db, nil
}

func rigMain() int {
	if i := execDoor(os.Args, os.Getenv(sched.LandlockEnv)); i >= 0 {
		if err := sched.ApplyLandlock(os.Getenv(sched.LandlockEnv)); err != nil {
			fmt.Fprintln(os.Stderr, "rig:", err)
			return 1
		}
		runtime.LockOSThread()
		argv := os.Args[i+1:]
		if len(argv) == 0 {
			fmt.Fprintln(os.Stderr, "rig: -exec needs a command")
			return 1
		}
		resolved, err := exec.LookPath(argv[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "rig: -exec: %v\n", err)
			return 1
		}
		if err := syscall.Exec(resolved, argv, os.Environ()); err != nil {
			fmt.Fprintf(os.Stderr, "rig: -exec: %v\n", err)
			return 1
		}
	}

	baseURL := flag.String("base-url", "", "OpenAI-compatible endpoint base URL (the worker swap); precedence: flag > RIG_BASE_URL > settings.json baseUrl > the embedded default")
	model := flag.String("model", "", "model name; precedence: flag > RIG_MODEL > settings.json model (no default; a run without one refuses)")
	system := flag.String("system", "", "system prompt; precedence: flag > RIG_SYSTEM > settings.json system > the embedded default")
	allow := flag.String("allow", "", "comma-separated allow-list of tool names; precedence: flag > RIG_ALLOW > settings.json allow > the embedded default")
	rounds := flag.Int("rounds", 0, "tool-call rounds per turn (0 is no cap); precedence: flag > RIG_ROUNDS > settings.json rounds")
	resultCap := flag.Int("result-cap", 0, "bytes a tool result may carry; precedence: flag > RIG_RESULT_CAP > settings.json resultCap")
	retries := flag.Int("retries", 0, "repetition bound on identical failing calls (cleared on success); precedence: flag > RIG_RETRIES > settings.json retries > the embedded default")
	effort := flag.String("effort", "", "reasoning effort level for the session's requests; precedence: flag > RIG_EFFORT > the model row")
	prompt := flag.String("p", "", "one-shot: run the single prompt and exit (the scheduler's worker path)")
	resumeID := flag.String("resume", "", "resume the session with this id (the transcript, the file provenance, and the identity rebuild from the state rows)")
	sessionID := flag.String("session-id", "", "set the fresh session identity (worker use)")
	tuiMode := flag.String("tui", "auto", "the terminal frontend: auto (default; when stdout is a terminal), true (force it), false (the piped CLI)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	updateFlag := flag.Bool("update", false, "update the binary to the latest release and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("rig %s\n", Version)
		return 0
	}

	if *updateFlag {
		cfg, err := defaultUpdateCfg()
		if err == nil {
			if home, herr := rigHome(); herr == nil {
				cfg.home = home
				if loaded, lerr := config.Load(home); lerr == nil {
					cfg.key = loaded.Settings.UpdateKey
				}
			}
			if v := os.Getenv("RIG_UPDATE_KEY"); v != "" {
				cfg.key = v
			}
			err = update(context.Background(), cfg)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "rig:", err)
			return 1
		}
		return 0
	}

	if *prompt != "" {
		signal.Ignore(syscall.SIGPIPE)
	}

	if len(os.Args) > 1 && os.Args[1] == "run-job" {
		os.Exit(runJob(os.Args[2:]))
	}

	if len(os.Args) > 1 && os.Args[1] == "decision" {
		os.Exit(decisionTrain(os.Args[2:], newTrainKernel))
	}

	serveAddr := ""
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		addr, code := parseServe(os.Args[2:])
		if code >= 0 {
			os.Exit(code)
		}
		serveAddr = addr
		if *prompt != "" {
			fmt.Fprintln(os.Stderr, "rig serve: -p is not available with serve (the page is the prompt)")
			os.Exit(2)
		}
	}

	if serveAddr == "" {
		if rest := flag.Args(); len(rest) > 0 {
			fmt.Fprintf(os.Stderr, "rig: %q is not a command (run-job, decision, serve)\n", rest[0])
			os.Exit(2)
		}
	}

	if *prompt == sched.PromptStdin {
		text, err := io.ReadAll(io.LimitReader(os.Stdin, file.ReadCap+1))
		if err != nil {
			fmt.Fprintln(os.Stderr, "rig: -p -:", err)
			os.Exit(2)
		}
		if int64(len(text)) > file.ReadCap {
			fmt.Fprintf(os.Stderr, "rig: -p -: the prompt on stdin is %d bytes, over the %d ceiling (the read tool's own cap): put it in a file and pass the path\n", len(text), file.ReadCap)
			os.Exit(2)
		}
		if strings.TrimSpace(string(text)) == "" {
			fmt.Fprintln(os.Stderr, "rig: -p -: nothing on stdin (the prompt arrives there)")
			os.Exit(2)
		}
		*prompt = string(text)
	}
	if err := checkOneShot(*prompt, *resumeID); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(2)
	}
	if err := checkSessionID(*sessionID, *resumeID); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}

	cfgDir, err := rigHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	cfg, err := config.Load(cfgDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	for _, n := range cfg.Notices {
		fmt.Fprintln(os.Stderr, "rig:", n)
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
	if modelID == "" {
		fmt.Fprintln(os.Stderr, "rig: no model: set --model, RIG_MODEL, or the model key in settings.json; there is no embedded default")
		return 1
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
		if *allow == sched.NoToolsAllow {
			allowList = nil
		} else {
			allowList = splitCSV(*allow)
		}
	}
	envInt := func(key string, def int) (int, bool) {
		v := os.Getenv(key)
		if v == "" {
			return def, true
		}
		n, aerr := strconv.Atoi(v)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "rig: %s: expected an integer, got %q\n", key, v)
			return 0, false
		}
		if n < 0 {
			fmt.Fprintf(os.Stderr, "rig: %s: expected a non-negative integer, got %d\n", key, n)
			return 0, false
		}
		return n, true
	}
	retriesN := *retries
	if !passed["retries"] {
		n, ok := envInt("RIG_RETRIES", cfg.Settings.Retries)
		if !ok {
			return 1
		}
		retriesN = n
	}
	roundsN := *rounds
	if !passed["rounds"] {
		n, ok := envInt("RIG_ROUNDS", cfg.Settings.Rounds)
		if !ok {
			return 1
		}
		roundsN = n
	}
	resultCapN := *resultCap
	if !passed["result-cap"] {
		n, ok := envInt("RIG_RESULT_CAP", cfg.Settings.ResultCap)
		if !ok {
			return 1
		}
		resultCapN = n
	}

	row := resolveModel(modelID, cfg.Models)
	if os.Getenv(sched.DelegateEnv) != "" {
		row = row.Capped(cfg.Settings.WorkerWindow)
		if err := row.Check(); err != nil {
			fmt.Fprintf(os.Stderr, "rig: settings workerWindow %d: %v\n", cfg.Settings.WorkerWindow, err)
			return 1
		}
	}

	effortLevel := envOr("RIG_EFFORT", "")
	if passed["effort"] {
		effortLevel = *effort
	}
	if effortLevel != "" {
		if err := command.CheckEffort(row.Efforts, effortLevel, row.ID); err != nil {
			fmt.Fprintln(os.Stderr, "rig:", err)
			return 1
		}
	}

	engine, fleetroom := newFleet()
	voice := fleetroom.Add(rig.MemberDecision)
	var machine machineroom.Member
	kids := newChildren(cfgDir, cfg.Models, engine, func() {
		broadcast.Say(voice, "room", "rig was updated: this instance left the room, restart it", evt.LevelError)
		machine.Close()
	}, voice)
	machine, err = machineroom.New(cfgDir, Version, kids.places, kids.effects, func(n evt.Notice) {
		broadcast.Say(voice, n.Source, n.Text, n.Level)
		kids.left(n, machine)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	defer machine.Close()
	defer kids.close()
	if len(machine.Live()) == 0 {
		for _, id := range kids.resident(machine) {
			broadcast.Say(voice, "models", id+" is resident (its row's port answers); starting nothing")
		}
	}

	rdb, err := openStore("rem", remstore.FilePath(cfgDir), remstore.Statements(), remstore.SchemaVersion, remstore.Migration(cwd))
	if err != nil {
		return 1
	}
	defer rdb.DB.Close()

	schedHome := filepath.Join(cfgDir, "scheduler")
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	scdb, err := openStore("scheduler", filepath.Join(schedHome, "global.sqlite"), sched.Statements(), sched.SchemaVersion, sched.Migration(schedHome, cfgDir, self+" run-job", sched.RealCrontab("")))
	if err != nil {
		return 1
	}
	defer scdb.DB.Close()

	swapURL := cfg.Settings.SwapURL
	if v := os.Getenv("RIG_SWAP_URL"); v != "" {
		swapURL = v
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rt := runtimeTable(cfg.Models, modelID, row)
	python := envOr("RIG_PYTHON", cfg.Settings.Python)
	wiring := workerWiring{
		DB: scdb, Home: schedHome, RigHome: cfgDir, StateDir: filepath.Join(cfgDir, "sessions"),
		SwapURL: swapURL, WorkerCmd: []string{self}, DefaultModel: modelID,
		Sandbox: cfg.Settings.Sandbox, SandboxBinds: cfg.Settings.SandboxBinds, Python: python, Allow: allowList,
		Fetch: sched.RealFetch(0), Spawn: sched.RealSpawn, Models: func() models.Table { return rt }, Room: fleetroom,
	}
	piped := *prompt != ""
	handoff := evt.NewCap(engine, cfg.Settings.MaxWorkers)
	del := delegate.New(delegate.Opts{
		Ctx: ctx, Await: piped,
		DB: wiring.DB, Home: wiring.Home, RigHome: wiring.RigHome, StateDir: wiring.StateDir,
		SwapURL: wiring.SwapURL, WorkerCmd: wiring.WorkerCmd, DefaultModel: wiring.DefaultModel,
		Sandbox: wiring.Sandbox, SandboxBinds: wiring.SandboxBinds, Python: wiring.Python, Allow: wiring.Allow,
		Fetch: wiring.Fetch, Spawn: wiring.Spawn, Models: wiring.Models, Room: wiring.Room,
		Cap: handoff,
	})

	py := pythontool.New(del)
	if python != "" {
		py = pythontool.NewWith(python, pythontool.DefaultHost(), del, cwd)
	}
	defer py.Close()
	fmt.Fprintf(os.Stderr, "rig: python kernel host: %s\n", py.Host())

	proxy := webProxy(cfg.Settings.WebFetchProxy)
	traf := cfg.Settings.Trafilatura
	if v, ok := os.LookupEnv("RIG_TRAFILATURA"); ok {
		traf = &v
	}
	webTool := webtool.New(webtool.Config{
		Search:     webtool.SearchConfig{BaseURL: envOr("RIG_SEARXNG_URL", cfg.Settings.SearXNG)},
		Fetch:      webtool.FetchConfig{Proxy: proxy, Trafilatura: traf},
		Background: del,
	})

	pluginsDir := filepath.Join(cfgDir, "plugins")
	if err := os.MkdirAll(filepath.Join(pluginsDir, "pending"), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "rig: plugins: create the pending zone: %v\n", err)
	}
	pluginFiles, err := plugins.List(cfgDir, "plugins")
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	decisionURL := cfg.Settings.DecisionURL
	if v := os.Getenv("RIG_DECISION_URL"); v != "" {
		decisionURL = v
	}
	native := make(map[string]bool)
	for _, name := range effectiveNativeNames() {
		native[name] = true
	}
	if decisionURL != "" {
		native["decide"] = true
	}
	fleet, err := sched.Fleet()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	fleet, verdictOn := fleetEnds(fleet, allowList)
	if verdictOn {
		native["verdict"] = true
	}
	pluginReports := make([]plugins.Report, 0)
	if len(pluginFiles) > 0 {
		pluginReports, err = plugins.DiscoverChecked(context.Background(), py, pluginFiles, native, plugins.PluginContract)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rig:", err)
			return 1
		}
	}
	pluginReports = capPlugins(pluginReports, cfg.Settings.Plugins.Max)
	for _, rep := range pluginReports {
		if rep.Skipped {
			fmt.Fprintf(os.Stderr, "rig: plugins: %s: %s\n", filepath.Base(rep.File), rep.Reason)
		}
	}

	if err := plugins.Check(pluginReports, native); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}

	sdb, err := openStore("state", state.StorePath(cfgDir, cwd), state.Statements(), state.SchemaVersion, state.Migration())
	if err != nil {
		return 1
	}
	defer sdb.DB.Close()

	todoPath := todostore.FilePath(cfgDir)
	tdb, err := openStore("todo", todoPath, todostore.Statements(), todostore.SchemaVersion, todostore.Migration(cwd, filepath.Dir(todoPath)), todostore.ReviewMigration, todostore.EdgeMigration)
	if err != nil {
		return 1
	}
	defer tdb.DB.Close()

	decdb, err := openStore("decision", decisionstore.FilePath(cfgDir), decisionstore.Statements(), decisionstore.SchemaVersion, decisionstore.Migration())
	if err != nil {
		return 1
	}
	defer decdb.DB.Close()

	drec := decisionstore.Recorder{DB: decdb, Scope: scope.Key(cwd), Voice: voice}
	prio, prioErr := claimPriority(*prompt, serveAddr != "")
	if prioErr != nil {
		fmt.Fprintln(os.Stderr, "rig:", prioErr)
		return 1
	}

	r := &root{
		pluginMax:  cfg.Settings.Plugins.Max,
		baseURL:    baseURLV,
		system:     systemPrompt,
		agents:     cfg.Agents,
		allow:      allowList,
		retries:    retriesN,
		rounds:     roundsN,
		resultCap:  resultCapN,
		sdb:        sdb,
		drec:       drec,
		remDB:      rdb,
		cwd:        cwd,
		home:       userHome(),
		pluginsDir: pluginsDir,
		rigHome:    cfgDir,
		activeID:   modelID,
		row:        row,
		mesh:       cfg.Settings.Mesh != nil,
		effort:     effortLevel,
		runtime:    rt,

		approve:        firstNonEmpty(cfg.Settings.Approve, approve.Auto),
		approveDefault: firstNonEmpty(cfg.Settings.Approve, approve.Auto),
		theme:          themeName(cfg.Settings.Theme, cfg.Theme),
		themeDoc:       cfg.Theme,
		themeTrueColor: tuiTrueColor(),
		tools: map[string]core.Tool{
			"read": file.NewRead(), "write": file.NewWrite(), "edit": file.NewEdit(),
			"sessions": sessionstool.New(cfgDir, cwd),
		},
		pluginsHome: cfgDir,
		engine:      engine,
		room:        fleetroom,
		machine:     machine,
		kids:        kids,
		priority:    prio,
	}

	r.tools["scheduler"] = schedapi.New(scdb, sched.RealCrontab(""), self+" run-job", modelID, cfgDir)
	if verdictOn {
		r.tools["verdict"] = verdicttool.New(fleet)
	}
	r.tools["bash"] = bash.New(del)
	r.tools["python"] = py
	r.tools["web"] = webTool
	r.py = py
	stopWorkers := del.StopAll
	r.listen()
	delegateOn, swarmWhy := fleetWiring(sched.RealFetch(0), swapURL, modelID, cfg.Models, cfg.Settings.Workers == nil || *cfg.Settings.Workers)
	if delegateOn {
		r.tools["delegate"] = del
	}
	if delegateOn {
		r.swarm = swarm.New(swarm.Opts{
			TodoDB: tdb, SchedDB: wiring.DB, Home: wiring.Home,
			Project: func(ctx context.Context, session string) (todostore.Project, error) {
				return sessionQueue(ctx, sdb, cwd, session)
			},
			Cwd:       cwd,
			WorkerCmd: wiring.WorkerCmd, Fetch: wiring.Fetch, Spawn: wiring.Spawn, SwapURL: wiring.SwapURL,
			Sandbox: wiring.Sandbox, SandboxBinds: wiring.SandboxBinds, Python: wiring.Python, RigHome: wiring.RigHome, StateDir: wiring.StateDir,
			Allow: wiring.Allow, DefaultModel: wiring.DefaultModel,
			Models: func() models.Table { return r.runtime },
			Engine: r.engine, Room: r.room, Cap: handoff,
		})
	}
	r.swarmWhy = swarmWhy
	var todoWake func()
	if delegateOn {
		todoWake = r.swarm.Wake
	}
	r.tools["todo"] = todoapi.New(tdb, todoapi.Mode(piped), todoWake)

	r.natives = native
	r.eco = plugins.NewEcosystem(cfgDir, r.natives, py, r.swapPlugins, func() (string, error) {
		return command.RenderPlugins(r.pluginInfos, "", r.pluginsHome), nil
	})

	menus := newTrainerMenu(cfgDir, cwd)
	env := r.commandEnv()
	env.DecisionTrain = decisionTrainEnqueue(scdb, cfgDir, self, cwd, sched.RealCrontab(""), menus.stall)
	env.Trainers = menus.names

	var fe core.Frontend
	var webSrv *web.Server
	closeFrontend := func() {}
	if serveAddr != "" {
		var door machineroom.Door
		var tailnet string
		if cfg.Settings.Mesh != nil {
			remote, home := meshKeys(cfg.Models)
			mesh := cfg.Settings.Mesh
			d, derr := machineroom.NewDoor(machine, machineroom.MeshConfig(mesh.Listen, mesh.Peers, time.Duration(mesh.Period)*time.Millisecond, mesh.Window), remote, home, func(n evt.Notice) { fmt.Fprintln(os.Stderr, "rig:", n.Text) })
			if derr != nil {
				fmt.Fprintln(os.Stderr, "rig serve:", derr)
				return 1
			}
			door = d
			defer door.Close()
			if host, _, err := net.SplitHostPort(cfg.Settings.Mesh.Listen); err == nil {
				if _, port, err := net.SplitHostPort(serveAddr); err == nil {
					if addr := net.JoinHostPort(host, port); addr != serveAddr {
						tailnet = addr
					}
				}
			}
		}
		srv, werr := web.New(web.Options{
			Home: cfgDir, CWD: cwd, Models: cfg.Models,
			Machine: machine, Door: door, Tailnet: tailnet, Version: Version,
			Ready:   func(ctx context.Context, m models.Model) error { return kids.ready(ctx, machine, m) },
			Crontab: sched.RealCrontab(""), RunnerCmd: self + " run-job", Natives: nativeToolNames,
			Commands: command.All(), Env: env, Status: webStatus(r, sdb),
			StopWorkers: stopWorkers,
		})
		if werr != nil {
			fmt.Fprintln(os.Stderr, "rig serve:", werr)
			return 1
		}
		webSrv = srv
		fe = srv
		closeFrontend = func() { srv.Close() }
		defer closeFrontend()
	} else if *prompt != "" {
		if err := oneshot.ErrPrompt(*prompt); err != nil {
			fmt.Fprintln(os.Stderr, "rig:", err)
			return 1
		}
		fe = &oneshot.OneShot{Prompt: *prompt, Out: os.Stdout, Err: os.Stderr, Fleet: fleet}
	} else if *tuiMode == "true" || (*tuiMode == "auto" && tui.IsTerminal(os.Stdout.Fd())) {
		th, terr := tui.ResolveTheme(cfg.Settings.Theme, cfg.Theme, tuiTrueColor())
		if terr != nil {
			fmt.Fprintln(os.Stderr, "rig:", terr)
			return 1
		}
		var idleStop func()
		if stopWorkers != nil || decisionURL != "" {
			idleStop = func() {
				if stopWorkers != nil {
					stopWorkers()
				}
				if rev := r.decRev; rev != nil {
					rev.Halt()
				}
			}
		}
		fe = tui.New(os.Stdin, os.Stdout, th,
			tui.WithStatus(tuiStatusIn(r, sdb)),
			tui.WithCommands(command.All(), env),
			tui.WithIdleInterrupt(idleStop),
		)

		if c, ok := fe.(interface{ Close() }); ok {
			closeFrontend = c.Close
			defer closeFrontend()
		}
	} else {
		fe = cli.New(os.Stdin, os.Stdout, cli.WithCommands(command.All(), env))
	}

	var packScorer graph.Scorer
	if decisionURL != "" {
		dec, derr := decision.NewHTTP(decision.HTTPOptions{URL: decisionURL})
		if derr != nil {
			fmt.Fprintln(os.Stderr, "rig: decision:", derr)
			return 1
		}
		dtool, derr := decision.NewDecide(decision.DecideOptions{
			Decider:  dec,
			Recorder: r.drec,
			Parallel: rig.DefaultParallel,
		})
		if derr != nil {
			fmt.Fprintln(os.Stderr, "rig: decision:", derr)
			return 1
		}
		r.decide = dtool
		reviews := &dbReviews{db: decdb}
		sink := &dbSink{db: decdb, scope: scope.Key(cwd)}
		_, headless := fe.(*oneshot.OneShot)
		var land func()
		if headless {
			r.decQ = decision.NewQueue(dec, sink, reviews, drec, nil, voice)
		} else {
			rev := decision.NewReviewer(ctx, r.engine, reviews,
				r.reviewFire(schedHome, scdb, swapURL, self, cfgDir, cfg.Settings.Sandbox, cfg.Settings.SandboxBinds, python, sched.Delegate),
				cfg.Settings.ReviewBatchOrDefault(), row, fleetroom, scope.Key(cwd))
			r.decRev = rev
			r.decQ = decision.NewQueue(dec, sink, reviews, drec, rev.Land, voice)
			land = rev.Land
		}
		psc, perr := decision.NewPackScorer(dec, sink, land, voice, rig.DefaultParallel)
		if perr != nil {
			fmt.Fprintln(os.Stderr, "rig: decision:", perr)
			return 1
		}
		packScorer = psc
		r.proposals = r.decQ
	}
	opts := []graph.Option{graph.WithPackCaps(graph.ReadCeiling, resultCapN)}
	if packScorer != nil {
		opts = append(opts, graph.WithScorer(packScorer))
	}
	gq := graph.NewQueue(cfgDir, fleetroom.Add(rig.MemberGraph), opts...)
	r.graph = gq
	r.tools["rem"] = remapi.New(rdb, gq)

	session, err := sessionFor(*resumeID, func(id string) (*core.Session, error) {
		return state.Resume(context.Background(), sdb, id)
	})
	if err != nil {
		closeFrontend()
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	if *sessionID != "" {
		session.ID = *sessionID
	}
	r.session = session
	r.fe = fe

	if a, ok := fe.(interface {
		Ask(ctx context.Context, prompt string) bool
	}); ok {
		r.askDoor = a.Ask
	}
	startupRows, serr := state.ListSessions(context.Background(), sdb, state.ListCap)
	if serr != nil {
		fmt.Fprintln(os.Stderr, "rig: todo queue:", serr)
	}
	proj := projectForRow(startupRows, cwd, session.ID)
	if note, e := reapClaims(context.Background(), tdb, startupRows, proj, session.ID); e != nil {
		fmt.Fprintln(os.Stderr, "rig: todo reap:", e)
	} else if note != "" {
		fmt.Fprintln(os.Stderr, "rig: todo:", note)
	}
	rec := state.NewRecorder(fe, sdb, cwd, modelID, Version, session.ID, session).Snapshot(file.SnapshotFiles)
	r.rec = rec

	k := wire(r)
	if _, perr := r.swapPlugins(context.Background(), pluginReports); perr != nil {
		fmt.Fprintln(os.Stderr, "rig: plugins:", perr)
		return 1
	}

	go gq.Run(ctx)
	if r.decQ != nil {
		go r.decQ.Run(ctx)
	}

	if webSrv != nil {
		fmt.Fprintf(os.Stderr, "rig serve: the dashboard is at http://%s/\n", serveAddr)
		go func() {
			if err := webSrv.ListenAndServe(ctx, serveAddr); err != nil {
				fmt.Fprintln(os.Stderr, "rig serve:", err)
			}
			stop()
		}()
	}

	runErr := loop.Run(ctx, k)
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "rig:", runErr)
	}
	if r.swarm != nil && len(r.swarm.List()) > 0 {
		if _, err := r.swarm.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "rig: swarm stop: %v\n", err)
		}
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
		closeFrontend()
		return 1
	}
	return 0
}
func main() { os.Exit(rigMain()) }
