package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3"
	"github.com/mrsirg97-rgb/rig/v3/config"
	"github.com/mrsirg97-rgb/rig/v3/decision"
	"github.com/mrsirg97-rgb/rig/v3/models"
	machineroom "github.com/mrsirg97-rgb/rig/v3/room"
	decisionstore "github.com/mrsirg97-rgb/rig/v3/store/decision"
	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
)

func runJob(args []string) int {
	fs := flag.NewFlagSet("rig run-job", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, "rig: usage: run-job <key>") }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "rig: usage: run-job <key>")
		return 2
	}
	if len(rest) > 1 {
		fmt.Fprintf(os.Stderr, "rig: run-job: unexpected %q (the key is the one positional; the job's knobs live in its row)\n", rest[1])
		return 2
	}
	key := rest[0]
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
	swapURL := cfg.Settings.SwapURL
	if v := os.Getenv("RIG_SWAP_URL"); v != "" {
		swapURL = v
	}
	python := cfg.Settings.Python
	if v := os.Getenv("RIG_PYTHON"); v != "" {
		python = v
	}
	ffmpeg := cfg.Settings.FFmpeg
	if v := os.Getenv("RIG_FFMPEG"); v != "" {
		ffmpeg = v
	}
	browser := cfg.Settings.Browser
	if v := os.Getenv("RIG_BROWSER"); v != "" {
		browser = v
	}
	model := cfg.Settings.Model
	if v := os.Getenv("RIG_MODEL"); v != "" {
		model = v
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
	decPath := decisionstore.FilePath(cfgDir)
	var jobDecisions decision.Recorder
	engine, fleetroom := newFleet()
	go engine.Start(context.Background())
	defer engine.Stop()
	voice := fleetroom.Add(rig.MemberDecision)
	kids := newChildren(cfgDir, cfg.Models, engine, nil, voice)
	var machine machineroom.Member
	if m, merr := machineroom.New(cfgDir, Version, kids.places, kids.effects, nil); merr != nil {
		fmt.Fprintln(os.Stderr, "rig:", merr)
	} else {
		machine = m
		defer machine.Close()
	}
	fleetroom.Add(rig.MemberFrontend).Subscribe(context.Background(), func(err error, messages ...broadcast.Message) {
		for _, m := range messages {
			if n, ok := m.Signal().(evt.Notice); err == nil && ok {
				fmt.Fprintln(os.Stderr, "rig: "+n.Source+": "+n.Text)
			}
		}
	})
	tell := jobTeller(machine, voice)
	if err := os.MkdirAll(filepath.Dir(decPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision store:", err)
	} else if jdb, jerr := openStore("decision", decPath, decisionstore.Statements(), decisionstore.SchemaVersion, decisionstore.Migration()); jerr != nil {
	} else {
		defer jdb.DB.Close()
		jobDecisions = decisionstore.Recorder{DB: jdb, Voice: voice}
	}
	if err := sched.RunJob(key, sched.RunOpts{
		Home:      home,
		Crontab:   sched.RealCrontab(""),
		Fetch:     sched.RealFetch(0),
		Spawn:     sched.RealSpawn,
		WorkerCmd: []string{self},
		SwapURL:   swapURL,

		Sandbox:      cfg.Settings.Sandbox,
		SandboxBinds: cfg.Settings.SandboxBinds,
		Python:       python,
		FFmpeg:       ffmpeg,
		Browser:      browser,
		RigHome:      cfgDir,
		StateDir:     filepath.Join(cfgDir, "sessions"),
		Models:       func() models.Table { return cfg.Models },
		DefaultModel: model,
		Decisions:    jobDecisions,
		Tell:         tell,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	return 0
}

func jobTeller(machine machineroom.Member, voice broadcast.Member) func(machineroom.JobStatus) {
	gate := machine != nil && machine.Admits(machineroom.JobStatusSince)
	return func(status machineroom.JobStatus) {
		if !gate {
			return
		}
		posted := make(chan struct{})
		machine.Act(status, machineroom.KeyOf("job/"+status.Job()), machineroom.PriorityFleet, func(bool) { close(posted) })
		if status.State() == machineroom.JobRunning {
			return
		}
		select {
		case <-posted:
		case <-time.After(machineroom.JobStatusPostWait):
			broadcast.Say(voice, "room", fmt.Sprintf("job %s: the %s status went unacknowledged after %s; the runner proceeds and the status may be lost",
				status.Job(), status.State(), machineroom.JobStatusPostWait), evt.LevelError)
		}
	}
}
