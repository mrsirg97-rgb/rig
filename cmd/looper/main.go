// Command looper is the composition root: every dependency explicit in one
// call, wired once at startup. Flags and env only; no config files.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/mrsirg97-rgb/looper"
	"github.com/mrsirg97-rgb/looper/frontend/cli"
	"github.com/mrsirg97-rgb/looper/loop"
	"github.com/mrsirg97-rgb/looper/middleware/guard"
	"github.com/mrsirg97-rgb/looper/middleware/perm"
	"github.com/mrsirg97-rgb/looper/policy"
	"github.com/mrsirg97-rgb/looper/provider/openai"
	"github.com/mrsirg97-rgb/looper/tool/bash"
	"github.com/mrsirg97-rgb/looper/tool/file"
)

// Version is the binary's release version; initial release per the stack.
const Version = "0.1.0"

const defaultSystem = "You are looper, a minimal coding agent. Use the provided tools to inspect, change, and run things in the working directory. Answer in plain text when done."

// wire assembles the kernel's dependencies. Swapping a seam is a change
// here and nowhere else.
func wire(baseURL, model, system string, allow []string, retries int) *looper.Kernel {
	return looper.New(
		looper.WithProvider(openai.New(baseURL, model)),
		looper.WithFrontend(cli.New(os.Stdin, os.Stdout)),
		looper.WithPolicy(policy.Passthrough(system)),
		looper.WithTools(bash.New(), file.Read(), file.Write(), file.Edit()),
		looper.WithMiddleware(
			perm.Allowlist(allow...),
			guard.Bound(retries),
		),
	)
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
	baseURL := flag.String("base-url", envOr("LOOPER_BASE_URL", "http://127.0.0.1:8080/v1"), "OpenAI-compatible endpoint base URL")
	model := flag.String("model", envOr("LOOPER_MODEL", "local"), "model name")
	system := flag.String("system", envOr("LOOPER_SYSTEM", defaultSystem), "system prompt")
	allow := flag.String("allow", envOr("LOOPER_ALLOW", "bash,read,write,edit"), "comma-separated allow-list of tool names")
	retries := flag.Int("retries", envOrInt("LOOPER_RETRIES", 3), "repetition bound on identical failing calls (cleared on success)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("looper %s\n", Version)
		return
	}

	k := wire(*baseURL, *model, *system, splitCSV(*allow), *retries)

	// Interrupt cancels the turn at its next boundary.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := loop.Run(ctx, k); err != nil {
		fmt.Fprintln(os.Stderr, "looper:", err)
		os.Exit(1)
	}
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
