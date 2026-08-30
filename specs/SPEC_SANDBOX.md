# rig: the sandbox (the containment the surfaces assume)

The runtime hardened before the surfaces grow further. rig executes
the model's bash, its python (a persistent kernel), its file writes,
and; since SPEC_PLUGINS; python the model itself may author, all
with the operator's privileges. Interactively that is a choice the
operator watches; on the scheduler's cron it is a choice nobody
watches, with the plugin forge (SPEC_PLUGINS 8, gated on this spec) it
is a choice that persists across sessions. This spec is the
containment: the worker jail, the plugin provenance rule, and the
egress split; each behind an existing seam, `core/` and `loop/`
byte-identical, the freeze holds.

The gate it opens, named: SPEC_PLUGINS decision 8 (the reload and the
forge) ships only after this spec's provenance rule (2) lands.

## the threat model, named

- **The unattended worker** (the sharpest edge): a `run-job` worker
  acts on a prompt written in the past, on content fetched from
  outside, with no operator present. A prompt-injected page, a
  poisoned repo file, or a plain model mistake runs `bash` with the
  operator's whole home. The worker is where containment pays first.
- **The forged capability**: a model that can write
  `~/.rig/plugins/*.py` mints tools that run in every future session
  (SPEC_PLUGINS 8). Creation must be separated from installation.
- **The injected interactive turn**: fetched web content steering the
  live session's tools. The operator is present (the loud tool rows
  are the defense rig already has), so interactive stays unjailed:
  but the egress path is guarded regardless (3).
- Out of scope, named: a malicious operator (it is their machine), a
  malicious rig binary (the supply chain is the operator's build), and
  kernel-grade escapes (bwrap's user namespaces are the boundary this
  spec buys; hardening past that is not a harness's job).

## goals

- The worker jail (1): `run-job` workers execute their subprocess
  tools (bash, the python kernel; plugins ride the kernel) inside a
  bubblewrap sandbox: the job's cwd read-write, the system read-only,
  no home, no rig home, a private /tmp, no network namespace. Fail
  closed: no `bwrap` on the box, no jailed worker; a loud refusal,
  not a silent unjailed run.
- The plugin provenance rule (2): model-authored plugins land in
  `plugins/pending/`, invisible to discovery; only the operator's
  `/plugins approve <name>` installs one. The model's `write` cannot
  land a file in `plugins/` directly (the perm middleware's one new
  path rule).
- The egress split (3): compute is jailed and netless: egress is a
  tool the host process runs, with the guard rails it already has
  (the web tool's loopback refusal, the proxy). The jail never needs
  network because the network is not the jail's to have.
- Interactive unchanged (4): the operator present is the sandbox;
  today's behavior, byte-identical, zero new prompts.
- One escape hatch, operator-only, named (5): `sandbox.binds` in
  settings for the devices and paths a jailed worker legitimately
  needs (`/dev/nvidia*` for a GPU plugin, a data directory). And
  `sandbox: "off"`; the operator's explicit act, one loud line per
  worker run, never a default.

## non-goals

- No interactive jail: the operator watching the loud tool rows is
  the interactive containment; jailing the daily driver breaks the
  work it exists for. A future opt-in is a later amendment, not this.
- No seccomp profiles, no LSM policies, no syscall filtering beyond
  what bwrap's defaults carry: the marginal safety is not worth the
  debugging surface on one operator's machine.
- No containers, no images, no docker: bwrap is a setuid-less
  namespace wrapper, one binary, no daemon; the smallest thing that
  is a real boundary.
- No in-jail network filtering (an allowlisted netns, slirp, an
  eBPF egress map): the split (3) makes it unnecessary; the jail has
  no network at all, and the egress tool's guard is one process, one
  policy, already written.
- No per-tool jails: one jail per worker process tree. bash and the
  kernel share it; splitting them buys nothing (they share a cwd
  anyway) and doubles the mount plumbing.
- No Windows, no macOS: bwrap is Linux: the spec says so and the
  refusal names it. (macOS sandbox-exec is deprecated; a later
  amendment if a real second platform appears.)
- No signing, no plugin hashes, no lockfile: provenance is a
  directory boundary and an operator verb, not a PKI.

## decisions

### 1. The worker jail: bwrap, one profile, fail closed

`run-job` spawns its worker as `rig -p` today (SPEC_PLUGINS non-goal:
the worker runs its own discovery in its own home). Amended: the
runner spawns the worker under `bwrap` when the job's profile says
`jailed` (the default). The jail, exactly:

```
bwrap
  --unshare-all --die-with-parent
  --ro-bind /usr /usr --ro-bind /lib /lib --ro-bind /lib64 /lib64
  --ro-bind /bin /bin --ro-bind /sbin /sbin --ro-bind /etc /etc
  --proc /proc --dev /dev --tmpfs /tmp
  --bind <job cwd> <job cwd>
  --ro-bind <rig home>/kernel <rig home>/kernel
  --ro-bind <the rig binary> <the rig binary>
  [--ro-bind or --bind per sandbox.binds]
  --chdir <job cwd>
  -- <the rig binary> -p <the job's prompt...>
```

- The whole worker process tree is inside: bash, the kernel, the
  kernel's children, a plugin's subprocesses. One boundary.
- `--unshare-all` includes the network namespace: the jail is
  netless. The worker's model calls do not survive that, so the
  provider's endpoint is the one hole, punched the narrow way: the
  runner binds a unix socket proxy into the jail (`--bind` of one
  socket file) that forwards to the llama-swap loopback endpoint, and
  the worker's base URL points at it. One socket, one destination,
  no other network. Rejected, named: `--share-net` (the whole
  loopback and the LAN with it; the injection's exfiltration path);
  a TCP slirp (a network stack to configure; the socket is a file).
- The stores: the worker's rig home is NOT bound. The worker's
  session store, todo, rem live in a scratch home inside the jail
  (`RIG_HOME=<cwd>/.rig-job` or a tmpfs path; the runner sets it);
  the job's REPORT lands through the runner (the scheduler's existing
  outcome row), which runs outside the jail. A worker that cannot
  write the operator's stores cannot poison the next session's
  transcript. The cost, named: a worker's rem reflections do not
  persist to the shared store; the report is the deliverable.
- Fail closed: profile `jailed` and no bwrap on `$PATH` refuses the
  run loud (the outcome row carries the refusal); `sandbox: "off"` in
  settings flips the default to unjailed with one loud line per run.
  The interactive REPL never consults any of this (4).

### 2. The plugin provenance rule (the forge's gate)

Creation is separated from installation:

- `~/.rig/plugins/*.py`, top level, is the TRUSTED set: discovery
  loads it (SPEC_PLUGINS 2). Placement there is the operator's act.
- `~/.rig/plugins/pending/` is the FORGE's landing zone: invisible to
  discovery by the existing top-level rule; no loader change at all.
  The model's authoring flows (SPEC_PLUGINS 8) write here.
- The perm middleware gains one path rule: the model's `write` and
  `edit` refuse a target inside `<rig home>/plugins/` that is not
  inside `plugins/pending/`. The refusal teaches: "plugins install by
  the operator's /plugins approve; write to plugins/pending/". The
  rule lives in `middleware/perm` beside the allowlist; the seam
  that already owns tool-call policy. Bash can still move a file
  there (the operator's shell is the operator's), which is why the
  rule is a guard for the honest path, not a security boundary
  against a determined model; the jail (1) is the boundary; the
  provenance rule is the workflow.
- `/plugins approve <name>`: moves `pending/<name>.py` to the top
  level and reloads (SPEC_PLUGINS 8's reload). `/plugins pending`
  lists the zone with each file's DESCRIPTION so the operator reads
  before blessing. Approval is the operator's verb: it never runs
  from a tool call (the command door is Frontend-side by
  construction; SPEC_COMMANDS 1 already guarantees this).

**The voices, named (PR B).** The path rule's refusal: `permission
denied: <path> is in plugins/ outside plugins/pending/ (plugins install
by the operator's /plugins approve; write to plugins/pending/)`.
`/plugins pending` is `plugins: N pending` with rows
`  <name>: <DESCRIPTION> (<file>)`; each file's top-level DESCRIPTION
string literal, read without running the file (a pending file is
untrusted, and the read is not the moment to execute it; a file without
one shows `(no DESCRIPTION)`), and `plugins: no pending plugins` for
an empty or absent zone. `/plugins approve <name>` checks in order and
refuses loud: the pending file (absent, named); a name that collides
with a native tool (the existing collision rule at the new door, its
voice: `plugins: name collision: "<name>" (<name>.py) is already a
native tool`); a file of the name already at the top level (a clobber
is not an operator's verb by accident: `plugins: approve: "<name>" is
already installed (<path> exists; remove it to install the pending
one)`). The move is the atomic rename, named on success (`plugins:
approved <name> (<from> -> <to>)`), and post-SPEC_PLUGINS-8 the
approved plugin's reload rides the same verb: its reply follows the
move's line, and a reload failure after the move keeps the move (the
disk is the truth) and names the failure (`...; the reload failed:
<reason>`). A pre-8 root (no reload seam) is the move only (`...; the
discovery loads it at the next start`). The zone is a fact of the
home: created at startup, silent and idempotent; the write tool makes
no directories, so the model's first pending write must not depend on
the operator's mkdir. Usage: `plugins: usage: plugins | plugins
pending | plugins approve <name> | plugins reload | plugins create
<text>`.

### 3. The egress split

Compute is jailed and netless (1); egress is the host's, guarded:

- `web_search` and `web_fetch` execute in the rig process (they
  already do; they are Go, not kernel python), outside the jail,
  with their existing rails: the loopback/private-range refusal, the
  proxy, the trafilatura pass. Nothing new lands here; the decision
  is that nothing needs to.
- A jailed worker that wants the network must want it through those
  two tools, where the policy lives. bash's `curl` and the kernel's
  `urllib` simply fail (no namespace); the loud teaching failure.
- The model-call socket (1) is not egress: it reaches exactly the
  provider and nothing else.

### 4. Interactive: the operator is the sandbox

The REPL and `-p` run as today: unjailed, the allowlist and the
guard as their middleware, the loud tool rows as their audit. The
plugin provenance rule (2) applies everywhere (it is a perm rule, not
a jail feature); the jail applies to `run-job` only. The line is the
attendance, and it is already the models table's vocabulary: the
`worker` role is the unattended one.

### 5. The settings, exactly

```
"sandbox": "jailed" | "off"        (default "jailed"; workers only)
"sandboxBinds": ["/dev/nvidia0", "/dev/nvidiactl", ...]
                                   (default []; ro-bind unless the
                                    entry ends ":rw"; workers only)
```

Two keys, the settings chain as SPEC_CONFIG 2 (no env, no flags: the
file over the embedded default). The refusal voice teaches both.

## testing

- the jail: a jailed fixture job whose prompt runs `bash ls ~`: the
  home is absent; `bash curl 127.0.0.1:8090`; no network; a write
  inside the cwd lands; a write outside refuses; the kernel's python
  sees the same walls (one boundary, both tools).
- the socket: the jailed worker completes a model call through the
  bound socket against a scripted provider; nothing else answers.
- fail closed: bwrap absent (PATH scrubbed): the run refuses loud
  and the outcome row carries it; `sandbox: "off"` runs unjailed with
  the one loud line.
- the scratch home: the worker's stores land inside the jail: the
  operator's `~/.rig` mtimes are untouched after the run.
- provenance: the model's write into `plugins/x.py` refuses with the
  teaching voice; into `plugins/pending/x.py` lands; discovery never
  loads pending; `/plugins pending` lists it with its DESCRIPTION;
  `/plugins approve x` moves and (post-SPEC_PLUGINS-8) reloads;
  approve of a name that collides with a native refuses (the existing
  rule at the new door).
- interactive untouched: the REPL golden path runs with zero sandbox
  code on it (no bwrap invocation recorded by a fake PATH shim).
- linux-only: the named refusal on a non-linux build (a build tag or
  a runtime check, the PR's choice, tested by the voice).

## scope

`middleware/perm` gains one path rule; `tool/scheduler`'s runner
gains the bwrap spawn and the socket proxy; `config` gains two keys;
`command/plugins.go` gains `pending` and `approve` verbs; docs name
bwrap as the one environment dependency of jailed workers (as git is
the diff tool's). `core/` and `loop/` byte-identical. No new Go
dependencies: the socket proxy is stdlib, bwrap is exec'd.

PR A is this file. PR B implements decision 2 (provenance; small,
unblocks SPEC_PLUGINS 8). PR C implements decisions 1/3/5 (the jail).
The order is deliberate: the forge's gate first, the worker jail
second, the reload (SPEC_PLUGINS 8) only after both.
