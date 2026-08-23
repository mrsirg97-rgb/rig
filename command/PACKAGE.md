# command

## What it is

The user-command leaf (SPEC_COMMANDS): the prefix rule, the `Env` the
root builds, and the standard set — one file per command, testable with
fakes: no kernel, no stores, no provider. Stdlib plus core and models,
nothing else: the leaf depends on core and models; the root depends on
the leaf.

## What it includes

- The prefix rule (`IsCommandLine`), the escape (`Unescape`), and the
  line splitter (`Parse`).
- `All()` — the standard set of twelve commands: `compact`, `new`,
  `models`, `sessions`, `steer`, `todo`, `scheduler`, `plugins`,
  `rem`, `effort`, `role`, `approve`.
- `Env` — the command's world, built at the root: closures, not handles.
- `EnvOf` — the type assertion on the dispatcher's env.
- `Sub` / `Subber` — the TUI's argument-hints door (SPEC_TUI 9).
- `Steerer` — the frontend-owned seam (the slot, the interrupt handle,
  the liveness fact).
- `SessionRow`, `PluginInfo` — the sessions and plugins rows.
- Renderers: `renderTable`, `renderList`, `RenderShow`, `RenderPlugins`.

## How it is consumed

- A Frontend with command support dispatches by prefix before `Input`
  returns; the loop never sees a command. `Env` rides the dispatcher's
  call as `any`; `EnvOf` asserts it is `*command.Env`.
- `All()` is registered at the root with one line; a new command is one
  file here and one line there, zero loop lines.
- `Env` is closures over the root's state, read at call time: a swap
  (the models switch, the plugins reload) is visible with no re-wiring.
- `Steer` is filled by the dispatcher in its `WithCommands`, not the
  root; `compact`/`new`/`sessions resume` read `LiveTurn` through it.

## Command semantics

- **compact** — forces the policy's `Compact` seam (the trigger bypassed
  on purpose: the trigger is the model's window math, the verb is the
  user's judgment).
- **models** — lists the runtime table, or switches the active model for
  the next turn (the switch rebuilds the provider+policy pair at the
  root; the loop reads `k.Provider`/`k.Policy` fresh at each turn start).
- **new** — closes the current session row ok, mints a fresh session and
  recorder, swaps them into the kernel; the steering slot is dropped.
- **sessions** — lists, shows, resumes over the rows that exist; its
  `Sub()` hints are `list`, `show`, `resume` (the TUI's verb menu), and
  `list` is the bare command's read under a name.
- **steer** — 7's slot, made a verb: queue the text (latest wins) and
  interrupt a live turn if one is; queue only if not. Empty interrupts
  only.
- **todo/scheduler** — a thin shared adapter over one of the model's own
  tools: parse the line into the tool's JSON args, call `Exec` with the
  session threaded, return the reply verbatim. `todo project <path>` is
  a one-off read of another project's queue (the path resolves through
  `store/scope`); writes stay the model's or the session's own bare
  verbs, and an unknown path's empty queue names the scope it read.
- **rem** — the operator's verb over the memory store (SPEC_STATE: rem is
  deliberate): `rem [list|show|forget]` — list the live memories
  (project then global, one line each), show by id, forget by id. The
  model's learn/recall/reflect/prune stays the
  tool; the operator's read and prune get a typed line.
- **plugins** — lists the loaded/skipped plugins and the pending zone;
  `approve <name>` installs a pending plugin; `reload` re-registers from
  disk; `create <text>` queues the authoring prompt; `enable <name>` /
  `disable <name>` move the file between `plugins/` and `plugins/disabled/`
  and reload (SPEC_GROWTH 9, amended); `disabled` lists that zone
  (SPEC_GROWTH 9, the hide/turn-off surface).

## Gotchas

- `Env` carries closures, not handles; nil seams refuse with the
  no-seam voice. A foreign env type is a wiring error named where it is
  found.
- `liveTurn` is nil-safe: a nil `Steerer` reads `LiveTurn` as false.
- A mid-turn `compact` rewrite races the loop's own read of the
  transcript — the command refuses while a turn is live.
- `Sub` is frontend-only; the CLI's dispatch ignores it.
- `approve` is the operator's verb, Frontend-side by construction (it
  never runs from a tool call). Post-8 its tail is the reload's; a reload
  failure keeps the move (the disk is the truth) and names the failure.
- `create` queues the authoring prompt through the steer slot; it never
  dispatches a turn itself.
- The pending listing reads each file's `DESCRIPTION` without executing
  it (a pending file is untrusted); the two common escapes are undone.
- `sessions resume` validates before mutate: the live turn and the
  current-id refusals come before the current row is touched.
- `renderTable`/`renderList`/`RenderShow`/`RenderPlugins` keep stable
  order (sorted by id / store order), so golden lines do not depend on
  merge order.
- `/effort`'s hints are positional: the words are the model's (the
  row's `efforts`, in its order), the one-liners are generic, so the
  descriptions describe position, never semantics. A row without
  efforts is a dial that is off.
- `/role`'s prose sits between the system prompt and AGENTS.md — the
  runtime's identity first, the stance second, the operator's contract
  third — and never outranks the contract: position is precedence, and
  AGENTS.md reads after it. `default` injects nothing. `/approve` is
  effective at the very next tool call (the gate reads the dial at call
  time; no request prefix moves, no re-prefill).
