package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"github.com/mrsirg97-rgb/rig/v2/command"
	"github.com/mrsirg97-rgb/rig/v2/config"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/frontend/cli"
	"github.com/mrsirg97-rgb/rig/v2/frontend/oneshot"
	"github.com/mrsirg97-rgb/rig/v2/frontend/tui"
	"github.com/mrsirg97-rgb/rig/v2/frontend/web"
	"github.com/mrsirg97-rgb/rig/v2/loop"
	"github.com/mrsirg97-rgb/rig/v2/middleware/approve"
	"github.com/mrsirg97-rgb/rig/v2/models"
	"github.com/mrsirg97-rgb/rig/v2/plugins"
	"github.com/mrsirg97-rgb/rig/v2/store"
	remstore "github.com/mrsirg97-rgb/rig/v2/store/rem"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v2/store/state"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
	"github.com/mrsirg97-rgb/rig/v2/swarm"
	"github.com/mrsirg97-rgb/rig/v2/tool/bash"
	"github.com/mrsirg97-rgb/rig/v2/tool/delegate"
	"github.com/mrsirg97-rgb/rig/v2/tool/file"
	pythontool "github.com/mrsirg97-rgb/rig/v2/tool/python"
	remapi "github.com/mrsirg97-rgb/rig/v2/tool/rem"
	schedapi "github.com/mrsirg97-rgb/rig/v2/tool/scheduler"
	sessionstool "github.com/mrsirg97-rgb/rig/v2/tool/sessions"
	todoapi "github.com/mrsirg97-rgb/rig/v2/tool/todo"
	webtool "github.com/mrsirg97-rgb/rig/v2/tool/web"
)

const Version = "2.1.5"

func main() {
	if i := execDoor(os.Args, os.Getenv(sched.LandlockEnv)); i >= 0 {
		if err := sched.ApplyLandlock(os.Getenv(sched.LandlockEnv)); err != nil {
			fmt.Fprintln(os.Stderr, "rig:", err)
			os.Exit(1)
		}
		runtime.LockOSThread()
		argv := os.Args[i+1:]
		if len(argv) == 0 {
			fmt.Fprintln(os.Stderr, "rig: -exec needs a command")
			os.Exit(1)
		}
		resolved, err := exec.LookPath(argv[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "rig: -exec: %v\n", err)
			os.Exit(1)
		}
		if err := syscall.Exec(resolved, argv, os.Environ()); err != nil {
			fmt.Fprintf(os.Stderr, "rig: -exec: %v\n", err)
			os.Exit(1)
		}
	}

	baseURL := flag.String("base-url", "", "OpenAI-compatible endpoint base URL (the worker swap); precedence: flag > RIG_BASE_URL > settings.json baseUrl > the embedded default")
	model := flag.String("model", "", "model name; precedence: flag > RIG_MODEL > settings.json model (no default; a run without one refuses)")
	system := flag.String("system", "", "system prompt; precedence: flag > RIG_SYSTEM > settings.json system > the embedded default")
	allow := flag.String("allow", "", "comma-separated allow-list of tool names; precedence: flag > RIG_ALLOW > settings.json allow > the embedded default")
	retries := flag.Int("retries", 0, "repetition bound on identical failing calls (cleared on success); precedence: flag > RIG_RETRIES > settings.json retries > the embedded default")
	prompt := flag.String("p", "", "one-shot: run the single prompt and exit (the scheduler's worker path)")
	resumeID := flag.String("resume", "", "resume the session with this id (the transcript, the file provenance, and the identity rebuild from the state rows)")
	sessionID := flag.String("session-id", "", "set the fresh session identity (worker use)")
	tuiMode := flag.String("tui", "auto", "the terminal frontend: auto (default; when stdout is a terminal), true (force it), false (the piped CLI)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	updateFlag := flag.Bool("update", false, "update the binary to the latest release and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("rig %s\n", Version)
		return
	}

	if *updateFlag {
		cfg, err := defaultUpdateCfg()
		if err == nil {
			if home, herr := rigHome(); herr == nil {
				if loaded, lerr := config.Load(home, "."); lerr == nil {
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
			os.Exit(1)
		}
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "run-job" {
		os.Exit(runJob(os.Args[2:]))
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
	if cfg.Notice != "" {
		fmt.Fprintln(os.Stderr, "rig:", cfg.Notice)
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
		os.Exit(1)
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
	envInt := func(key string, def int) int {
		v := os.Getenv(key)
		if v == "" {
			return def
		}
		n, aerr := strconv.Atoi(v)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "rig: %s: expected an integer, got %q\n", key, v)
			os.Exit(1)
		}
		if n < 0 {
			fmt.Fprintf(os.Stderr, "rig: %s: expected a non-negative integer, got %d\n", key, n)
			os.Exit(1)
		}
		return n
	}
	retriesN := envInt("RIG_RETRIES", cfg.Settings.Retries)
	if passed["retries"] {
		retriesN = *retries
	}
	roundsN := envInt("RIG_ROUNDS", cfg.Settings.Rounds)
	resultCapN := envInt("RIG_RESULT_CAP", cfg.Settings.ResultCap)

	row := resolveModel(modelID, cfg.Models)

	py := pythontool.New()
	if python := envOr("RIG_PYTHON", cfg.Settings.Python); python != "" {
		py = pythontool.NewWith(python, pythontool.DefaultHost())
	}
	py.SetCwd(cwd)
	defer py.Close()
	fmt.Fprintf(os.Stderr, "rig: python kernel host: %s\n", py.Host())

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
	webTool := webtool.New(webtool.Config{
		Search: webtool.SearchConfig{BaseURL: envOr("RIG_SEARXNG_URL", cfg.Settings.SearXNG)},
		Fetch:  webtool.FetchConfig{Proxy: proxy, Trafilatura: traf},
	})

	pluginsDir := filepath.Join(cfgDir, "plugins")
	if err := os.MkdirAll(filepath.Join(pluginsDir, "pending"), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "rig: plugins: create the pending zone: %v\n", err)
	}
	pluginFiles, err := plugins.List(cfgDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	native := make(map[string]bool)
	for _, name := range effectiveNativeNames(cfg.Workers) {
		native[name] = true
	}
	pluginReports := make([]plugins.Report, 0)
	if len(pluginFiles) > 0 {
		pluginReports, err = plugins.DiscoverChecked(context.Background(), py, pluginFiles, native)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rig:", err)
			os.Exit(1)
		}
	}
	pluginTools := make([]core.Tool, 0, len(pluginReports))
	pluginInfos := make([]command.PluginInfo, 0, len(pluginReports))
	pluginReports = capPlugins(pluginReports, cfg.Settings.Plugins.Max)
	for _, rep := range pluginReports {
		info := command.PluginInfo{
			Name: rep.Name, Description: rep.Description, File: rep.File,
			Skipped: rep.Skipped, Reason: rep.Reason,
		}
		pluginInfos = append(pluginInfos, info)
		if info.Skipped {
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
	sdb, quarantined, sReport, err := store.Open(sessionsPath, state.Statements(), state.SchemaVersion, state.Migration())
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig: state store:", err)
		os.Exit(1)
	}
	if quarantined != "" {
		fmt.Fprintf(os.Stderr, "rig: quarantined corrupt state file: %s\n", quarantined)
	}
	if sReport != "" {
		fmt.Fprintln(os.Stderr, "rig:", sReport)
	}
	defer sdb.DB.Close()

	todoPath := todostore.FilePath(cfgDir)
	if err := os.MkdirAll(filepath.Dir(todoPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	tdb, todoQuarantined, todoReport, todoErr := store.Open(todoPath, todostore.Statements(), todostore.SchemaVersion, todostore.Migration(cwd, filepath.Dir(todoPath)), todostore.ReviewMigration, todostore.EdgeMigration)
	if todoErr != nil {
		fmt.Fprintln(os.Stderr, "rig: todo store:", todoErr)
		os.Exit(1)
	}
	if todoQuarantined != "" {
		fmt.Fprintf(os.Stderr, "rig: quarantined corrupt todo file: %s\n", todoQuarantined)
	}
	if todoReport != "" {
		fmt.Fprintf(os.Stderr, "rig: %s\n", todoReport)
	}
	defer tdb.DB.Close()

	remPath := remstore.FilePath(cfgDir)
	if err := os.MkdirAll(filepath.Dir(remPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	rdb, remQuarantined, remReport, remErr := store.Open(remPath, remstore.Statements(), remstore.SchemaVersion, remstore.Migration(cwd))
	if remErr != nil {
		fmt.Fprintln(os.Stderr, "rig: rem store:", remErr)
		os.Exit(1)
	}
	if remQuarantined != "" {
		fmt.Fprintf(os.Stderr, "rig: quarantined corrupt rem file: %s\n", remQuarantined)
	}
	if remReport != "" {
		fmt.Fprintf(os.Stderr, "rig: %s\n", remReport)
	}
	defer rdb.DB.Close()

	schedHome := filepath.Join(cfgDir, "scheduler")
	if err := os.MkdirAll(schedHome, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	scdb, sQuarantined, sReport, sErr := store.Open(filepath.Join(schedHome, "global.sqlite"), sched.Statements(), sched.SchemaVersion, sched.Migration(schedHome, cfgDir, self+" run-job", sched.RealCrontab("")))
	if sErr != nil {
		fmt.Fprintln(os.Stderr, "rig: scheduler store:", sErr)
		os.Exit(1)
	}
	if sQuarantined != "" {
		fmt.Fprintf(os.Stderr, "rig: quarantined corrupt scheduler file: %s\n", sQuarantined)
	}
	if sReport != "" {
		fmt.Fprintf(os.Stderr, "rig: %s\n", sReport)
	}
	defer scdb.DB.Close()

	swapURL := cfg.Settings.SwapURL
	if v := os.Getenv("RIG_SWAP_URL"); v != "" {
		swapURL = v
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
		remDB:      rdb,
		cwd:        cwd,
		home:       userHome(),
		pluginsDir: pluginsDir,
		rigHome:    cfgDir,
		activeID:   modelID,
		row:        row,
		runtime:    runtimeTable(cfg.Models, modelID, row),

		approve:        firstNonEmpty(cfg.Settings.Approve, approve.Auto),
		approveDefault: firstNonEmpty(cfg.Settings.Approve, approve.Auto),
		tools: map[string]core.Tool{
			"bash": bash.New(), "read": file.Read(), "write": file.Write(), "edit": file.Edit(),
			"todo": todoapi.New(tdb, todoapi.Mode(*prompt != "")), "rem": remapi.New(rdb),
			"python": py, "web": webTool,
			"sessions": sessionstool.New(cfgDir, cwd),
		},
		workers:     cfg.Workers,
		pluginTools: pluginTools,
		py:          py,
		pluginsHome: cfgDir,
		pluginInfos: pluginInfos,
	}

	if workers := cfg.Workers; workers != nil {
		r.tools["scheduler"] = schedapi.New(scdb, sched.RealCrontab(""), self+" run-job", workers.Model, cfgDir)
		r.tools["delegate"] = delegate.New(delegate.Opts{
			DB:           scdb,
			Home:         schedHome,
			RigHome:      cfgDir,
			StateDir:     filepath.Join(cfgDir, "sessions"),
			SwapURL:      swapURL,
			WorkerCmd:    []string{self},
			DefaultModel: workers.Model,
			Slots:        workers.Slots,
			Sandbox:      cfg.Settings.Sandbox,
			SandboxBinds: cfg.Settings.SandboxBinds,
			Allow:        allowList,
			Fetch:        sched.RealFetch(0),
			Spawn:        sched.RealSpawn,
			Models:       func() models.Table { return r.runtime },
			Notify:       func(ev core.Event) { r.rec.Notify(ev) },
		})
		r.swarm = swarm.New(swarm.Opts{
			TodoDB:  tdb,
			SchedDB: scdb,
			Home:    schedHome,
			Project: func(ctx context.Context, session string) (todostore.Project, error) {
				return sessionQueue(ctx, tdb, cwd, session)
			},
			Cwd:           cwd,
			WorkerCmd:     []string{self},
			Fetch:         sched.RealFetch(0),
			Spawn:         sched.RealSpawn,
			SwapURL:       swapURL,
			Sandbox:       cfg.Settings.Sandbox,
			SandboxBinds:  cfg.Settings.SandboxBinds,
			RigHome:       cfgDir,
			StateDir:      filepath.Join(cfgDir, "sessions"),
			Allow:         allowList,
			FleetModel:    workers.Model,
			ReviewerModel: workers.Reviewer,
			Models:        func() models.Table { return r.runtime },
			Frontend:      func() core.Frontend { return r.rec },
		})
	}

	for _, t := range pluginTools {
		r.tools[t.Name()] = t
	}

	r.natives = native
	r.tools["plugins"] = plugins.NewEcosystem(cfgDir, r.natives, py, r.swapPlugins, func() (string, error) {
		return command.RenderPlugins(r.pluginInfos, "", r.pluginsHome), nil
	})

	env := r.commandEnv()

	var fe core.Frontend
	var webSrv *web.Server
	closeFrontend := func() {}
	if serveAddr != "" {
		srv, werr := web.New(web.Options{
			Home: cfgDir, CWD: cwd, Models: cfg.Models, Workers: cfg.Workers,
			Crontab: sched.RealCrontab(""), RunnerCmd: self + " run-job", Natives: nativeToolNames,
			Commands: command.All(), Env: env, Status: webStatus(r, sdb),
		})
		if werr != nil {
			fmt.Fprintln(os.Stderr, "rig serve:", werr)
			os.Exit(1)
		}
		webSrv = srv
		fe = srv
		closeFrontend = func() { srv.Close() }
		defer closeFrontend()
	} else if *prompt != "" {
		if err := oneshot.ErrPrompt(*prompt); err != nil {
			fmt.Fprintln(os.Stderr, "rig:", err)
			os.Exit(1)
		}
		fe = &oneshot.OneShot{Prompt: *prompt, Out: os.Stdout, Err: os.Stderr}
	} else if *tuiMode == "true" || (*tuiMode == "auto" && tui.IsTerminal(os.Stdout.Fd())) {
		th, terr := tui.ResolveTheme(cfg.Settings.Theme, cfg.Theme, tuiTrueColor())
		if terr != nil {
			fmt.Fprintln(os.Stderr, "rig:", terr)
			os.Exit(1)
		}
		fe = tui.New(os.Stdin, os.Stdout, th,
			tui.WithStatus(tuiStatusIn(r, sdb)),
			tui.WithCommands(command.All(), env),
		)

		if c, ok := fe.(interface{ Close() }); ok {
			closeFrontend = c.Close
			defer closeFrontend()
		}
	} else {
		fe = cli.New(os.Stdin, os.Stdout, cli.WithCommands(command.All(), env))
	}

	session, err := sessionFor(*resumeID, func(id string) (*core.Session, error) {
		return state.Resume(context.Background(), sdb, id)
	})
	if err != nil {
		closeFrontend()
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
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
	proj, perr := sessionQueue(context.Background(), tdb, cwd, session.ID)
	if perr != nil {
		fmt.Fprintln(os.Stderr, "rig: todo queue:", perr)
	}
	if note, e := reapClaims(context.Background(), sdb, tdb, cwd, proj, session.ID); e != nil {
		fmt.Fprintln(os.Stderr, "rig: todo reap:", e)
	} else if note != "" {
		fmt.Fprintln(os.Stderr, "rig: todo:", note)
	}
	rec := state.NewRecorder(fe, sdb, cwd, modelID, Version, session.ID, session).Snapshot(file.SnapshotFiles)
	r.rec = rec

	k := wire(r)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	if cfg.Notice != "" {
		fmt.Fprintln(os.Stderr, "rig:", cfg.Notice)
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
		StateDir:     filepath.Join(cfgDir, "sessions"),
		Models:       func() models.Table { return cfg.Models },
	}); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	return 0
}
