# rig as a Go module

rig is a module: `github.com/mrsirg97-rgb/rig`. The binary at
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

## the five seams

`cmd/rig/main.go` reduces to five seams, wired once:

| seam | what it is | root wiring |
|---|---|---|
| `Provider` | one turn, streamed | `openai.New(...)` / `openai.NewWithConfig(...)` |
| `Tools` | the tool table | the map of `tool/*` adapters, plus `pluginTools` |
| `Frontend` | input pull, event notify | `tui.New`, `cli.New`, or `oneshot.New` |
| `Policy` | message assembly | `compact.New` (the per-model trigger) |
| `Middleware` | the tool-exec chain | toolset, approve, paths, perm, guard |

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
  mid-turn tool effect shows stale until the next command.
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

	"github.com/mrsirg97-rgb/rig"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/loop"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/todo"
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
		reply, err := todo.Create(ctx, tdb, proj, []todo.CreateItem{{Text: "sum the file"}}, session)
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

`core/` and `loop/` are the frozen surface. The freeze gate
(`frontend/tui/freeze_test.go`) refuses a real change there unless the
branch name carries `-refactor` and the PR names the reopening.

- **core/** is frozen at 1.5.0's bytes. The 1.5.0 hosted-mode reopening
  (SPEC_HOSTED: `Usage.Cost`, `ReasoningDelta.Details`,
  `Message.ReasoningDetails`) is closed; the next core change opens it
  by name.
- **loop/** is open to pure addition, closed to modification, with named
  reopenings (the batch's concurrent reads, the panic recovery, the
  fed-back error line). Each has its own gate clause and re-freeze.

Pure additions (a new event type, a new seam method alongside the old)
land without reopening. Everything else needs the named change in the
PR and in SPEC_CORE or SPEC_EVT.

## local vs hosted

A model row says where it runs. Local: the row hits the swap at
`RIG_SWAP_URL` and the model's GPU slot gates it. Hosted
(`remote: true` or `provider: "openrouter"`): the row's `baseUrl` and
`apiKey` speak the OpenAI wire as-is, `Authorization: Bearer <key>`
rides every request, 429 and 5xx retry with bounded backoff, and the
worker skips the local swap and the busy probe entirely, bound instead
by the row's `concurrency` tokens. `usage.cost` lands in the state
store and sums into a swarm's `budget=` or a scheduled job's `budget`.
