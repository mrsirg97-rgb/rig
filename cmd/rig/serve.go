package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mrsirg97-rgb/rig/v2/frontend/web"
)

func parseServe(args []string) (string, int) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7777", "the bind address (loopback only: 127.0.0.1, ::1, or localhost; tailscale serve is the way out)")
	if err := fs.Parse(args); err != nil {
		return "", 2
	}
	if err := web.Loopback(*addr); err != nil {
		fmt.Fprintln(os.Stderr, "rig serve:", err)
		return "", 1
	}
	return *addr, -1
}
