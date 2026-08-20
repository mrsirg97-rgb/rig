# consolidation note

Result of the per-package refactor pass (rig-*-refactor branches) and the
post-refactor review of this codebase. Each package now carries its own
`PACKAGE.md` — the package spec and gotchas, with the prose removed from
the code. This note is the consolidation read: what streamlines, what
does not, the system-prompt audit, and the runtime security audit.

## package map (where each spec lives)

| package | spec | what it owns |
|---------|------|--------------|
| core | core/PACKAGE.md | the kernel's contract surface: seams, wire types, session, ctx helpers. Pure; no behavior. |
| loop | loop/PACKAGE.md | the one place turn ordering is written down. Concrete by design. |
| models | models/PACKAGE.md | the per-model table and row invariants (window math). |
| config | config/PACKAGE.md | the four-layer settings chain, the model table out of code, AGENTS/theme. |
| command | command/PACKAGE.md | the user-command leaf: prefix rule, the Env, the standard set. |
| middleware/guard | middleware/guard/PACKAGE.md | the retry bound (name-keyed, streak-clearing). |
| middleware/perm | middleware/perm/PACKAGE.md | the allow-list and the plugin provenance rule. |
| middleware/toolset | middleware/toolset/PACKAGE.md | the root's live tool table (the reload's swap). |
| policy | policy/PACKAGE.md | prompt assembly: the passthrough and the compact policy + overflow decorator. |
| provider/openai | provider/openai/PACKAGE.md | the OpenAI-compatible SSE adapter, unix-socket dial included. |
| plugins | plugins/PACKAGE.md | the python plugin surface: discovery, the tool seam, the reload. |
| store/, tool/ | (not yet spec'd) | persistence and the toolset. Out of scope of this pass. |

## streamlining read

The refactor's conclusion is that the code is well-factored; there is
little structural consolidation that is both safe and beneficial.

- The one named duplication is `normalizePath`: `middleware/perm/plugins.go`
  mirrors `tool/file/file.go` (the comment says "mirrors tool/file's").
  It is two ~5-line helpers; `perm` intentionally does not import `tool/file`
  (a middleware importing a tool is a seam smell). Centralizing into `core`
  would touch the contract surface for a trivial win; the honest call is to
  leave it and let the mirror drift if it ever matters. If it does, move both
  into a tiny shared `pathutil` package, not `core`.
- `middleware/guard`, `middleware/perm`, `middleware/toolset` are three
  separate leaves on purpose: distinct seams, each testable alone. Merging
  would couple them. Keep.
- `policy/compact` re-implements a 5-line `assemble` (system + transcript)
  that `policy/passthrough` also has; importing the parent package for that
  is a smell. The duplication is trivial. Keep.
- `toolset.Table.List()` and `plugins.Tool.File()` are exported but used
  only by tests. They are reasonable accessors; keep.
- The store/ and tool/ packages are untouched by this pass; they are the
  remaining refactor candidates (store/state, store/rem, store/scheduler,
  store/todo, and the tool/* leaves), each a separate rig-<pkg>-refactor
  branch when the process resumes.

Conclusion: no structural consolidation is worth applying. The seams are
the design; the package boundaries are the architecture.

## system-prompt audit

Surfaces reviewed: the embedded system prompt (`config/settings.json`
`system`), `policy/compact/summary_prompt.txt`, and the plugins forge's
`createTemplate` (`command/plugins.go`).

- The system prompt is lean and unambiguous: "You are rig, a minimal
  coding agent. Use the provided tools to inspect, change, and run things
  in the working directory. Answer in plain text when done." It states the
  contract (tool use), the domain (the working directory), and the output
  shape (plain text). No change — good is good.
- `summary_prompt.txt` is clear: third-person factual summary, fold prior
  summaries, "answer with the summary only". The one ambiguity worth
  noting: it tells the model to keep "what is still true" and drop "what
  is done" — fine as written; no change.
- `createTemplate` is terse to a fault: it references `SPEC_SANDBOX` (a
  spec label the model may not have) and assumes the model knows the
  `plugins_reload` contract. It is a steer prompt, not the system prompt;
  the reload's own Description carries the contract the model sees on the
  wire. Leave as-is — tightening would cost the model context it already
  gets from the tool descriptions.

## runtime security audit

Findings (green unless noted):

- **Deny by default**: `perm.Allowlist` refuses unlisted tool names; the
  embedded allow-list is the tool surface. Denials return the message as
  both content and error so `guard.Bound` can bound the repetition.
- **Provenance rule**: `perm.Plugins` canonicalizes paths the way the file
  tools do, so the model's `write`/`edit` to `plugins/` outside
  `plugins/pending/` is refused; the pending zone is invisible to
  discovery until the operator's `approve`.
- **Untrusted plugin reads**: the pending listing reads each file's
  `DESCRIPTION` without executing it. Discovery does import/execute (that
  is the loading), through the shared python kernel.
- **SSRF guards** (tool/web): host resolved before the fetch, private
  addresses refused (v4 private/loopback/broadcast, v6 loopback/ULA/
  link-local/multicast, IPv4-mapped v6 folded), re-checked across redirect
  hops, and hop count capped. Fail closed.
- **Fail-closed persistence**: `core.Load` uses `DisallowUnknownFields`
  and normalizes a missing `Files` to an empty map; `Save` writes 0600.
- **Worker jail**: the runner's bwrap profile fails closed by default
  (SPEC_SANDBOX); the jailed worker's model call reaches the host through
  the unix-socket proxy.
- **Shell**: `tool/bash` runs `bash -c` by design (the tool's purpose);
  the model authors the command string.
- Note: the provenance rule's path test is on the canonical string, not
  `EvalSymlinks`. A symlinked `pending` would be an edge bypass, but it
  requires an operator-created symlink inside the rig home; not a primary
  risk.

No runtime defect was found worth patching in this pass.
