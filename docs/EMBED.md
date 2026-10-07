# rig as a Go module

rig is a module: `github.com/mrsirg97-rgb/rig/v2`. The binary at
`cmd/rig` is one wiring of it. Import the module and the loop, the
seams, and the stores are yours.

## what you get

- **core**: the seam interfaces (`Provider`, `Frontend`, `ContextPolicy`,
  `Tool`, `ToolMiddleware`, `Command`) and the wire types (`Message`,
  `ToolCall`, `ToolSpec`, `Request`, `Usage`, the `Event` vocabulary).
- **loop**: `loop.Run(ctx, kernel)`, the concrete turn runtime. It
  assembles the context, streams the provider, executes tool calls
  through the middleware chain, and returns on `Done`, `Fault`, or
  context cancel.
- **evt**: the event engine one turn runs on. One consumer, many
  producers; tool calls run concurrently, effects land in call order.
- **kernel.go**: `rig.New(rig.WithProvider(...), ...)`, the composition
  root as a struct. Duplicate tool or command names panic at `New`.
- **store**: the SQLite stores (`state`, `todo`, `rem`, `scheduler`),
  the `sqlx` transaction seam (`store.Open`), and the project scope
  identity (`store/scope`).

## the kernel surface

`rig.New` takes nine options (`kernel.go`); the root wires seven of
them once (the command set rides the frontend dispatchers, and
`Parallel` stays at its default):

| option | what it is | root wiring |
|---|---|---|
| `WithProvider` | one turn, streamed | `openai.New(...)` / `openai.NewWithConfig(...)` |
| `WithTools` | the tool table | the map of `tool/*` adapters, plus `pluginTools` |
| `WithFrontend` | input pull, event notify | `tui.New`, `cli.New`, or `oneshot.New` |
| `WithPolicy` | message assembly | `compact.New` (the per-model trigger) |
| `WithCommands` | the slash-command set | unwired at the root; commands dispatch at the frontend seam |
| `WithMiddleware` | the tool-exec chain | toolset, approve, cutoff, perm, guard, paths; the graph tap inside, the decision links outside |
| `WithConcurrent` | the batch's admit predicate | the concurrent natives: read, view, web, delegate, decide |
| `WithParallel` | the in-flight bound | unset; `DefaultParallel` is 8 |
| `WithEngine` | the event engine | the root's engine, the fleet room's queue |

Beside the options the kernel names the queue's five priorities
(`PriorityInput` 90, `PriorityStream` 50, `PriorityTool` 50,
`PriorityFleet` 30, `PriorityReview` 10) and the room's four member
ids (`MemberFrontend`, `MemberDelegate`, `MemberGraph`,
`MemberDecision`).

The loop never names a concrete tool, provider, policy, frontend, or
middleware. One file plus one registration line extends it.

## the TUI options

`tui.New` takes the embedder's doors as options:

- `tui.WithStatus(fn)` supplies the startup block's and the status
  row's numbers (model, effort, window, session up/down/cache) and,
  through `StatusIn.Rows`, the footer band: the embedder's rows under a
  dim rule below the status line (nothing when empty). The status
  recaptures after every successful command (the Used reset stays at
  `/new` and `sessions resume`); rows change at command time only, so a
  mid-turn tool effect shows stale until the next command. Add
  `tui.WithStatusTick(d)` to re-read the function every d while the
  TUI waits for input: a background write the function sees lands on
  the next tick, and the region redraws only when the rows changed
  (zero is off; the tick never fires mid-turn).
- `tui.WithCommands(cmds, env)` wires the slash-command dispatch and
  the steering seam.
- `tui.WithTitle(name, rows, tagline)` replaces the welcome block's
  "rig" block letters with `rows` and adds `tagline` as a line under
  them (default: the rig rows, no tagline). The ascii glyph fallback
  prints `name` as the plain row.

## worked example: an HTTP service, one process

A kernel, a board-backed job from `store/todo`, and an in-process
worker (`loop.Run` in a goroutine). No subprocesses, no second binary.

```go
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/loop"
	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/todo"
)

type frontend struct {
	prompt string
	events []core.Event
}

func (f *frontend) Input(ctx context.Context) (string, error) { return f.prompt, nil }
func (f *frontend) Notify(ev core.Event) { f.events = append(f.events, ev) }

type passthrough struct{}

func (passthrough) Assemble(ctx context.Context, s *core.Session) ([]core.Message, error) {
	return s.Messages, nil
}

type stubProvider struct{}

func (stubProvider) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	ch := make(chan core.Event, 2)
	ch <- core.TextDelta{Text: "done"}
	ch <- core.Done{StopReason: "stop"}
	close(ch)
	return ch, nil
}

func taskID(reply string) string { // the create reply carries the id in quotes
	start := strings.Index(reply, "'")
	if start < 0 {
		return ""
	}
	rest := reply[start+1:]
	end := strings.Index(rest, "'")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func main() {
	home := "/home/ng/.rig"
	cwd := "/home/ng/Projects/app"

	// The board-backed store: one sqlite file per scope, migrated on open.
	path := todo.FilePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatal(err)
	}
	tdb, _, _, err := store.Open(path, todo.Statements(), todo.SchemaVersion,
		todo.Migration(cwd, filepath.Dir(path)), todo.ReviewMigration, todo.EdgeMigration)
	if err != nil {
		log.Fatal(err)
	}
	defer tdb.DB.Close()

	session := core.NewSession().ID
	proj := todo.ProjectOf(cwd) // the repo's scope; worktrees share it

	http.HandleFunc("POST /job", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reply, err := todo.Create(ctx, tdb, proj, todo.CreateItem{Text: "sum the file"}, session)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		id := taskID(reply) // the minted id, e.g. tN

		f := &frontend{prompt: "do task " + id}
		k := rig.New(
			rig.WithProvider(stubProvider{}),
			rig.WithFrontend(f),
			rig.WithPolicy(passthrough{}),
		)
		// One process: the worker is a goroutine, not a spawn.
		go func() {
			if err := loop.Run(ctx, k); err != nil {
				log.Printf("worker: %v", err)
				return
			}
			if _, err := todo.Complete(ctx, tdb, proj, id, session, true); err != nil {
				log.Printf("complete: %v", err)
			}
		}()
		json.NewEncoder(w).Encode(map[string]string{"id": id})
	})

	log.Fatal(http.ListenAndServe("127.0.0.1:8080", nil))
}
```

The kernel holds no store handles. `todo.Create` and `todo.Complete`
are the board's verbs; the worker's `loop.Run` owns the turn. The
service is one process and the worker cannot outlive it.

## what the lock freezes

`core/` and `loop/` are the frozen surface. The freeze gate is
`specs/FREEZE.txt`, read by `cmd/freeze` in the freeze job of
`.github/workflows/ci.yml`: every path a PR touches must match a line
of the file, a real change to the frozen surface needs a
`reopen <path> <version>` line, and a branch carrying `-refactor`
skips the gate (the PR names the reopening).

- **core/** is frozen on the face `specs/FREEZE.txt` states: open to
  pure addition only (SPEC_CORE), every modification behind a
  `reopen <path> <version>` line in that file. The 1.5.0 hosted-mode
  reopening (SPEC_HOSTED: `Usage.Cost`, `ReasoningDelta.Details`,
  `Message.ReasoningDetails`) is closed. `core/provider.go` reopened
  at 2.8.3 and has grown since: `Snapshot`, `Phase` and `Verdict`
  ride the event vocabulary beside the wire events, so the frozen
  bytes are no longer 1.5.0's.
- **loop/** is open to pure addition, closed to modification, with named
  reopenings (the batch's concurrent reads, the panic recovery, the
  fed-back error line). Each has its own gate clause and re-freeze.

Pure additions (a new event type, a new seam method alongside the old)
land without reopening. Everything else needs the named change in the
PR and in SPEC_CORE or SPEC_EVT.

## local vs hosted

A model row says where it runs. Local: the row hits the swap at
`RIG_SWAP_URL` and the resident-set gate admits it (SPEC_WORKERS: the
fleet is the resident model; no slot is counted). Hosted
(`remote: true` or `provider: "openrouter"`): the row's `baseUrl` and
`apiKey` speak the OpenAI wire as-is, `Authorization: Bearer <key>`
rides every request, 429 and 5xx retry with bounded backoff, and the
worker skips the local swap and the busy probe entirely; the row's
`concurrency` key is retired (the config names it once at start:
`concurrency retired: the fleet is the resident model`), and the
endpoint's own 429 retry is the backpressure. `usage.cost` lands in
the state store and sums into a swarm's `budget=` or a scheduled
job's `budget`.
