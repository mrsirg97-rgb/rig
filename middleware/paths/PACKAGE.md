# middleware/paths

## What it is

The path boundary: one link in the root's chain that expands a leading
`~`, `~/…`, or `~user/…` in a tool call's path-shaped arguments before
any tool sees them. A model's path habits are half shell and half API;
the shell expands `~` and the tools did not, so `ls ~/Projects/x` failed
where `ls /home/x/Projects/x` worked — a footgun a model named from inside
rig on 2026-08-23, with the fix's shape: at the boundary, not per tool,
so every tool inherits it and nothing drifts.

## What it includes

- `Fields` — the path-shaped argument names: `path`, `root`, `cwd`. A new
  tool that names its path one of these inherits the expansion; one that
  invents a fourth name is a finding for this list, not a per-tool fix.
- `Expand(p)` — `~` and `~/…` are `os.UserHomeDir` (`$HOME`; the scratch
  home inside a jail); `~user` and `~user/…` are that user's home
  (`os/user`). An unknown user, an unset home, or a `~` anywhere else
  leave the path as given.
- `Rewrite(args)` — the top-level object's `Fields` that are strings and
  expand; bytes pass through untouched when nothing expands (the recorder
  and the approval prompt see what the model sent unless a `~` moved).
- `Middleware()` — the `core.ToolMiddleware` over `Rewrite`.

## How it is consumed

- Wired innermost in the root's chain (`cmd/rig`), so it runs after the
  allow-list, the gate and the bounds and right before the tool: a
  refused call is refused on the model's bytes; an executed call runs on
  the expanded path. Workers and delegated workers get the same chain.
- `bash` is also wrapped (its `cwd`), and its command line is the shell's
  own to expand. A plugin's nested `args` are the plugin's (python's
  `expanduser` is one line).
