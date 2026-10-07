# rig plugins

Python plugins as tools: one file under the rig home's `plugins/`
directory, one tool per file, the name the filename stem. A loaded
plugin is a real tool on the execution chain: the allow-list admits it
by its presence, the python tool can import it by the stem, and the
wire reaches it through the one `plugin` door — its name rides the
door's `name` enum, and its description and schema cross only when
`plugin` `schema` is called (2.8.2). The governing specs:
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
- **DESCRIPTION** (str) reaches the model verbatim, through the
  `plugin` door's `schema` action: write it for the model.
- **SCHEMA** (dict) is the tool's JSON schema.
- **run(args: dict) -> str** receives the model's args dict: the return
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

`plugins.max` (settings.json) caps the live set: a load that would
exceed it is skipped with the reason `disabled: over the settings.json
plugins.max cap`, the first N files (by name) win. The cap rides the
startup and every reload; the number is read with the settings at
startup, so raising it takes a restart.

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

**Through the model's door.** The `plugin` door's `create` action
takes a name, the source, and writes the pending file, checking the
name rule, the native collision, and the contract the same way the
command door does (the shared `WritePending`). Writing a pending file
that already exists updates it; the reply names which.

**From the dashboard.** The plugin forge reads a plugin's source and
saves the full contract file into the pending zone (create or update);
the plugin view lists all three zones with each file's DESCRIPTION,
read without running the file.

Discovery's failure voices, the same on every door:

- a file missing a name, or failing import, is a loud skip naming the
  file and the field; startup and the reload continue (a broken plugin
  must not brick the harness);
- a name colliding with a native tool refuses loud (native-wins would
  be silent shadowing), before the file executes; `plugin` is a native,
  so the name is reserved (`plugins` is not: the name folded into the
  door in 2.8.2);
- an invalid manually-installed filename is skipped before execution.

The writes and moves are symlink-hardened: `WritePending` refuses a
pending path that is already a symlink and opens with `O_NOFOLLOW`;
`Move` (approve, disable, enable) refuses a symlinked source before
the move and rechecks the destination after the rename, rolling back a
landing that is not the file it moved.

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
`specs/SPEC_STREAMLINE.md` 4); `/plugins reload` stays the operator's
explicit verb. From python, a plugin is a plain import by the stem.
The live names ride the door's `name` enum, so the model sees what is
callable without pulling each schema.

The operator's verbs, at `/plugins`:

- bare: the loaded plugins (name, description, file) and the skipped
  ones with their reasons;
- `pending`: the pending zone with each file's DESCRIPTION;
- `approve <name>`: move a pending plugin to the top level, replacing
  an installed one of the same name (the line says so) and reloading;
- `disabled`, `disable <name>`, `enable <name>`: the off switch;
- `create <text>`: queue the authoring prompt;
- `reload`: re-run the discovery: the new list is live on the next
  turn, and a failed reload leaves the table and the wire untouched.

The `plugin` door also carries the ecosystem verbs (`list`, `create`,
`delete`, `reload`) beside `run` and `schema`; `delete` is a move into
`plugins/disabled/`, not an unlink. `plugins` is not a tool: it folded
into `plugin` in 2.8.2.

## the allow-list

- The built-in default permits the built-in tools only: a plugin is not
  in it.
- An installed plugin's presence in `plugins/` root is its own
  allow-list entry: the operator's approve put it there, and the
  allow-list's second door (the live plugin table) admits it without an
  `allow` line.
- A plugin still in `plugins/pending/` is not live and stays refused
  until approved; the refusal names the tool and the allow-list.
- A `settings.json` that writes its own `allow` key replaces the
  built-in default whole, so it must carry `plugin` or every door
  call is refused. A `plugins` entry is dropped at the read with a
  notice: folded into `plugin` in 2.8.2, the name is dropped.

## the shared kernel

Discovery imports every file through the same persistent IPython
kernel the `python` tool uses: one process, the namespace shared on
purpose. The cost is named and accepted: the model's python can call
plugin functions directly, and plugin state persists across calls. A
plugin call's default timeout is 120 s, charged from the kernel's turn
(a queued call is never charged queue time); a kernel-level failure is
the call's error, and a per-file failure is a skipped report.

## the train zone

`train/` (2.13.0) sits beside `plugins/` and shares its machinery: the
same zones (`pending/`, `disabled/`), the same filename rule, a
contract checked before any code runs. A trainer file
(`~/.rig/train/<name>.py`, the name the stem) exposes two callables:

```python
def train(rows_path: str, out_dir: str) -> dict:
    ...  # returns a report naming its checkpoint: {"checkpoint": "/abs", "questions": {...}}

def evaluate(checkpoint: str, rows_path: str) -> dict:
    ...  # the same report shape, scored on the rows it is handed
```

The interpreter is separate: `trainPython` (settings.json) names the
train zone's own venv python — torch is two gigabytes, and the train
kernel is its own process, never the plugins' shared kernel. Unset, a
training run refuses.

The runner is `rig decision train <trainer>`, headless, never inside a
turn. `/decision train <trainer>` enqueues it as a scheduler command
job (once, a couple of minutes out); the nightly is your own cron
line. The gold rows are the reviewer's settled decision rows, split
deterministically four-to-one (train / held-out) into
`~/.rig/decision/train/<utc-tag>/rig-train.jsonl` and
`rig-heldout.jsonl`.

Promotion: the candidate must beat the constant baseline and the
incumbent on every question. On a win, the one `Environment=` line
carrying `RIG_DECISION_CHECKPOINT=` in the unit file named by
`decisionUnit` is rewritten — and nothing else is touched: rig does
not run `systemctl`; the reply prints the daemon-reload and restart
for you to run. Without a `decisionUnit` the run trains, records, and
promotes nothing (there is nothing served to beat). Every run's three
reports and the promoted word land in the decision store either way.
`specs/SPEC_DECISION.md` governs.

## trust

In the interactive REPL and one-shot runs the plugins run with rig's
privileges, in the operator's kernel: trust them as you trust your own
python. A jailed (`bwrap`) or landlock scheduled worker gets a scratch
home — `RIG_HOME=<cwd>/.rig-job` — and the operator's rig home is not
bound inside it (only the python host's `kernel/` directory is), so
the operator's plugins are never discovered by a jailed worker:
protection by absence, not by name (`specs/SPEC_SANDBOX.md` 1, 3, 5).
Only `sandbox: "off"` inherits the home, and its workers run with the
operator's plugins present. The provenance rule is the workflow: the
model's `write` and `edit` are admitted only when the resolved and the
lexical path agree the target sits in `plugins/pending/` — a write
landing in `plugins/` outside pending is refused, and so is a symlink
whose two views disagree about the zone; the refusal teaches the
shape. The jail is the boundary (the operator's shell is the
operator's).
