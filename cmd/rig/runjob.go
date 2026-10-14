package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3"
	"github.com/mrsirg97-rgb/rig/v3/config"
	"github.com/mrsirg97-rgb/rig/v3/decision"
	"github.com/mrsirg97-rgb/rig/v3/models"
	machineroom "github.com/mrsirg97-rgb/rig/v3/room"
	"github.com/mrsirg97-rgb/rig/v3/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v3/store/decision"
	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
)

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
	for _, n := range cfg.Notices {
		fmt.Fprintln(os.Stderr, "rig:", n)
	}
	swapURL := cfg.Settings.SwapURL
	if v := os.Getenv("RIG_SWAP_URL"); v != "" {
		swapURL = v
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
	if err := os.MkdirAll(filepath.Dir(decPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision store:", err)
	} else if jdb, _, _, jerr := store.Open(decPath, decisionstore.Statements(), decisionstore.SchemaVersion, decisionstore.Migration()); jerr != nil {
		fmt.Fprintln(os.Stderr, "rig: decision store:", jerr)
	} else {
		defer jdb.DB.Close()
		engine, fleetroom := newFleet()
		go engine.Start(context.Background())
		defer engine.Stop()
		kids := newChildren(cfgDir, cfg.Models, engine, nil)
		machine, merr := machineroom.New(cfgDir, kids.places, kids.effects, nil)
		if merr != nil {
			fmt.Fprintln(os.Stderr, "rig:", merr)
		} else {
			defer machine.Close()
		}
		fleetroom.Add(rig.MemberFrontend).Subscribe(context.Background(), func(err error, messages ...broadcast.Message) {
			for _, m := range messages {
				if n, ok := m.Signal().(evt.Notice); err == nil && ok {
					fmt.Fprintln(os.Stderr, "rig: "+n.Source+": "+n.Text)
				}
			}
		})
		jobDecisions = decisionstore.Recorder{DB: jdb, Voice: fleetroom.Add(rig.MemberDecision)}
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
		DefaultModel: model,
		Decisions:    jobDecisions,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	return 0
}
