# rig plugins

Python plugins as tools: one file under the rig home's `plugins/`
directory, one tool per file, the name the filename stem. A loaded
plugin is a real tool on the execution chain: the allow-list admits it
by its presence, the python tool can import it by the stem, and on the
wire it rides the `plugin` door beside the natives. The governing specs:
`specs/SPEC_PLUGINS.md` (the contract), `specs/SPEC_GROWTH.md` 9 (the
door), `specs/SPEC_SANDBOX.md` 2 (the provenance rule).

## the contract

A plugin is one `*.py` file with three top-level names:

```python
DESCRIPTION = "what the tool does, for the model"
SCHEMA = {"type": "object", "properties": {"text": {"type": "string"}}}

def run(args: dict) -> str:
    return "echo: " + args["text"]
```

- **The name** is the filename stem (`echo.py` -> `echo`), matching
  `^[a-z][a-z0-9_]{0,63}$` (a lowercase letter, then letters, digits,
  underscores).
- **DESCRIPTION** (str) rides the wire verbatim: write it for the
  model.
- **SCHEMA** (dict) is the tool's JSON schema.
- **run(args: dict) -> str** receives the model's args dict; the return
  value is the tool result. An exception is a tool error carrying the
  traceback tail, and the kernel stays alive (it is the model's kernel
  too).

The module is imported through the shared python kernel and kept
importable under the stem, so the `python` tool's imports reach the
loaded plugins and plugin state persists across calls. Reload replaces
the module in both the live table and `sys.modules`; disabled or removed
plugin modules leave both tables.

## the zones

| zone                        | what it is                                                        |
|-----------------------------|-------------------------------------------------------------------|
| `~/.rig/plugins/`           | the live plugins: top-level `*.py` only, in filename order        |
| `~/.rig/plugins/pending/`   | the forge's landing zone: where the model's authoring lands; invisible to discovery; created at startup, silent and idempotent |
| `~/.rig/plugins/disabled/`  | the off switch: a deleted or disabled plugin moves here, hidden and not callable, never deleted; `/plugins enable` brings it back |

A missing or empty `plugins/` directory is a no-op that never starts
the kernel: the wire is the built-in tools' bytes exactly.

## creating a plugin

Every path writes to the pending zone. Installation is the operator's
verb, never the model's.

**By hand.** Write the file into `~/.rig/plugins/pending/`, review it
as you would any python you will run, then approve:

```
/plugins approve echo
```

**From the command door.** `/plugins create <text>` queues the
authoring prompt (the steer precedent: the command queues a line, never
dispatches a turn). The model writes the file into the pending zone;
you review and approve.

**Through the model's tool.** The `plugins` tool's `create` action
takes a name and the source and writes the pending file, checking the
name rule, the native collision, and the contract the same way the
command door does (the shared `WritePending`).

**From the dashboard.** The plugin forge reads a plugin's source and
saves the full contract file into the pending zone (create or update);
the plugin view lists all three zones with each file's DESCRIPTION,
read without running the file.

Discovery's failure voices, the same on every door:

- a file missing a name, or failing import, is a loud skip naming the
  file and the field; startup and the reload continue (a broken plugin
  must not brick the harness);
- a name colliding with a native tool refuses loud (native-wins would
  be silent shadowing), before the file executes; `plugin` and `plugins`
  are natives, so both are reserved;
- an invalid manually-installed filename is skipped before execution.

## consuming a plugin

The request carries the built-in tools plus one `plugin` door; the
per-plugin schemas stay behind it, so a grown table stops blowing
context (`specs/SPEC_GROWTH.md` 9):

```
plugin {action: "schema", name: "echo"}         the contract, on demand
plugin {action: "run", name: "echo", args: {...}}  the call
```

An unknown name runs the root's reload once and re-resolves before
refusing (an out-of-band install is callable without a reload call,
`specs/SPEC_STREAMLINE.md` 4); `plugins reload` stays the operator's
explicit verb. From python, a plugin is a plain import by the stem.

The operator's verbs, at `/plugins`:

- bare: the loaded plugins (name, description, file) and the skipped
  ones with their reasons;
- `pending`: the pending zone with each file's DESCRIPTION;
- `approve <name>`: move a pending plugin to the top level;
- `disabled`, `disable <name>`, `enable <name>`: the off switch;
- `create <text>`: queue the authoring prompt;
- `reload`: re-run the discovery; the new list is live on the next
  turn, and a failed reload leaves the table and the wire untouched.

The model's `plugins` tool carries its own verbs (`list`, `create`,
`delete`, `reload`); `delete` is a move into `plugins/disabled/`, not
an unlink.

## the allow-list

- The built-in default permits the built-in tools only; a plugin is not
  in it.
- An installed plugin's presence in `plugins/` root is its own
  allow-list entry: the operator's approve put it there, and the
  allow-list's second door (the live plugin table) admits it without an
  `allow` line.
- A plugin still in `plugins/pending/` is not live and stays refused
  until approved; the refusal names the tool and the allow-list.
- A `settings.json` that writes its own `allow` key replaces the
  built-in default whole, so it must carry `plugin` and `plugins` or
  every door call is refused.

## the shared kernel

Discovery imports every file through the same persistent IPython
kernel the `python` tool uses: one process, the namespace shared on
purpose. The cost is named and accepted: the model's python can call
plugin functions directly, and plugin state persists across calls. A
plugin call's default timeout is 120 s, charged from the kernel's turn
(a queued call is never charged queue time); a kernel-level failure is
the call's error, and a per-file failure is a skipped report.

## trust

In the interactive REPL and one-shot runs the plugins run with rig's
privileges, in the operator's kernel: trust them as you trust your own
python. A scheduled worker's plugins run jailed under bwrap
(`specs/SPEC_SANDBOX.md` 1, 3, 5). The provenance rule is the workflow
(the model's `write` and `edit` to `plugins/` outside
`plugins/pending/` are refused, and the refusal teaches the shape); the
jail is the boundary (the operator's shell is the operator's).
