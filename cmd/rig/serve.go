package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mrsirg97-rgb/rig/config"
	"github.com/mrsirg97-rgb/rig/frontend/web"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

// serve is the dashboard subcommand (SPEC_SERVE): one file plus a
// registration line at the root (SPEC_SERVE 1). It resolves the rig home
// and the config (the models table), builds the dashboard, and serves it
// over loopback until interrupted.
func serve(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7777", "the bind address (loopback only: 127.0.0.1, ::1, or localhost; tailscale serve is the way out)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	home, err := rigHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	cfg, err := config.Load(home, cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	srv, err := web.New(web.Options{
		Home:    home,
		CWD:     cwd,
		Models:  cfg.Models,
		Crontab: sched.RealCrontab(""),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig serve:", err)
		return 1
	}
	defer srv.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(os.Stderr, "rig serve: the dashboard is at http://%s/\n", *addr)
	if err := srv.ListenAndServe(ctx, *addr); err != nil {
		fmt.Fprintln(os.Stderr, "rig serve:", err)
		return 1
	}
	return 0
}
