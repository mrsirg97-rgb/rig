// Command rig is the composition root: every dependency explicit in one
// call, wired once at startup. Flags and env only; no config files.
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

	"github.com/mrsirg97-rgb/rig"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/frontend/cli"
	"github.com/mrsirg97-rgb/rig/frontend/oneshot"
	"github.com/mrsirg97-rgb/rig/loop"
	"github.com/mrsirg97-rgb/rig/middleware/guard"
	"github.com/mrsirg97-rgb/rig/middleware/perm"
	"github.com/mrsirg97-rgb/rig/policy"
	"github.com/mrsirg97-rgb/rig/provider/openai"
	"github.com/mrsirg97-rgb/rig/store"
	remstore "github.com/mrsirg97-rgb/rig/store/rem"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	"github.com/mrsirg97-rgb/rig/store/state"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
	"github.com/mrsirg97-rgb/rig/tool/bash"
	"github.com/mrsirg97-rgb/rig/tool/file"
	"github.com/mrsirg97-rgb/rig/tool/fs"
	pythontool "github.com/mrsirg97-rgb/rig/tool/python"
	remapi "github.com/mrsirg97-rgb/rig/tool/rem"
	schedapi "github.com/mrsirg97-rgb/rig/tool/scheduler"
	todoapi "github.com/mrsirg97-rgb/rig/tool/todo"
	webtool "github.com/mrsirg97-rgb/rig/tool/web"
)

// Version is the binary's release version; initial release per the stack.
const Version = "0.1.0"

const defaultSystem = "You are rig, a minimal coding agent. Use the provided tools to inspect, change, and run things in the working directory. Answer in plain text when done."

// wire assembles the kernel's dependencies. Swapping a seam is a change
// here and nowhere else.
func wire(baseURL, model, system string, allow []string, retries int, fe core.Frontend, todoTool, remTool, schedTool, pyTool, webSearchTool, webFetchTool core.Tool) *rig.Kernel {
	mw := []core.ToolMiddleware{
		perm.Allowlist(allow...),
		guard.Bound(retries),
	}
	// the root's chain is [perm, guard]: the observation tap is retired —
	// the recorder now sources its rows from the loop's events.
	// The guidelines ride the system prompt, not the chain (decision 6):
	// prompt assembly belongs to the prompt, and the prompt string is the
	// root's — zero loop change.
	fullSystem := system
	if g := guidelinesOf(mw); g != "" {
		fullSystem = system + "\n\n" + g
	}
	return rig.New(
		rig.WithProvider(openai.New(baseURL, model)),
		rig.WithFrontend(fe),
		rig.WithPolicy(policy.Passthrough(fullSystem)),
		rig.WithTools(bash.New(), file.Read(), file.Write(), file.Edit(), fs.LS(), fs.Find(), fs.Grep(), todoTool, remTool, schedTool, pyTool, webSearchTool, webFetchTool),
		rig.WithMiddleware(mw...),
	)
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

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func main() {
	// the default endpoint is the worker swap itself: two defaults for the
	// same server is how the runner's busy check (on the swap) passes while
	// the worker (on another) faults every tick.
	baseURL := flag.String("base-url", envOr("RIG_BASE_URL", "http://127.0.0.1:8090/v1"), "OpenAI-compatible endpoint base URL (the worker swap)")
	model := flag.String("model", envOr("RIG_MODEL", "local"), "model name")
	system := flag.String("system", envOr("RIG_SYSTEM", defaultSystem), "system prompt")
	allow := flag.String("allow", envOr("RIG_ALLOW", "bash,read,write,edit,ls,find,grep,todo,rem,scheduler,python,web_search,web_fetch"), "comma-separated allow-list of tool names")
	retries := flag.Int("retries", envOrInt("RIG_RETRIES", 3), "repetition bound on identical failing calls (cleared on success)")
	prompt := flag.String("p", "", "one-shot: run the single prompt and exit (the scheduler's worker path)")
	resumeID := flag.String("resume", "", "resume the session with this id (the transcript, the file provenance, and the identity rebuild from the state rows)")
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

	// The transcript: workspace-shared sqlite under the user config
	// directory; one store, opened once, loud on corruption.
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	digest := sha1.Sum([]byte(cwd))
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	// The python kernel: one persistent IPython session for the process
	// (rig runs one session per process); the root owns its teardown —
	// pane's session_shutdown hook, without the hook. RIG_PYTHON is the
	// operator's interpreter (and the seam's contract: no lazy venv
	// bootstrap); the host choice is logged so pane's and rig's hosts
	// cannot drift silently.
	py := pythontool.New()
	if v := os.Getenv("RIG_PYTHON"); v != "" {
		py = pythontool.NewWith(v, pythontool.DefaultHost())
	}
	defer py.Close()
	fmt.Fprintf(os.Stderr, "rig: python kernel host: %s\n", py.Host())

	// The web tools: pane's web_search and web_fetch, one leaf package.
	// The env knobs are read here, the way flags and env live at the root:
	// RIG_SEARXNG_URL is the SearXNG instance (the web-tools compose
	// publishes one on loopback); RIG_WEB_FETCH_PROXY is the egress
	// proxy (pane's test semantics: set empty = direct); RIG_TRAFILATURA
	// is an explicit extraction binary (empty = the stdlib pass carries).
	webSearch := webtool.Search()
	if v := os.Getenv("RIG_SEARXNG_URL"); v != "" {
		webSearch = webtool.NewSearch(webtool.SearchConfig{BaseURL: v})
	}
	proxy := webtool.DefaultProxy
	if v, ok := os.LookupEnv("RIG_WEB_FETCH_PROXY"); ok {
		proxy = v
	}
	var traf *string
	if v, ok := os.LookupEnv("RIG_TRAFILATURA"); ok {
		traf = &v
	}
	webFetch := webtool.NewFetch(webtool.FetchConfig{Proxy: proxy, Trafilatura: traf})

	sessionsPath := filepath.Join(cfgDir, "rig", "sessions", hex.EncodeToString(digest[:6])+".sqlite")
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
	todoPath := filepath.Join(cfgDir, "rig", "todo", hex.EncodeToString(digest[:12])+".sqlite")
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

	// The memory store: the hybrid single file, pane's convention under
	// the user config directory; opened once, loud on corruption.
	remPath := filepath.Join(cfgDir, "rig", "rem", "rem.sqlite")
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
	schedHome := filepath.Join(cfgDir, "rig", "scheduler")
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

	// The frontend: the REPL by default; one-shot under -p (deliverable 2's
	// seam). Everything downstream of the swap is shared.
	var fe core.Frontend = cli.New(os.Stdin, os.Stdout)
	if *prompt != "" {
		if err := oneshot.ErrPrompt(*prompt); err != nil {
			fmt.Fprintln(os.Stderr, "rig:", err)
			os.Exit(1)
		}
		fe = &oneshot.OneShot{Prompt: *prompt, Out: os.Stdout}
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
	rec := state.NewRecorder(fe, sdb, cwd, *model, Version, session.ID, session)

	k := wire(*baseURL, *model, *system, splitCSV(*allow), *retries, rec, todoapi.New(tdb), remapi.New(rdb),
		schedapi.New(sched.Stores{Global: sgdb, Cwd: scdb}, sched.RealCrontab(""), self+" run-job"), py, webSearch, webFetch)
	k.Session = session // one identity: the loop's session is the transcript's

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
// the outcome. Recorded outcomes exit 0; loud refusals exit non-zero.
func runJob(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "rig: usage: run-job <key>")
		return 2
	}
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	home := filepath.Join(cfgDir, "rig", "scheduler")
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
		SwapURL:   envOr("RIG_SWAP_URL", ""),
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
