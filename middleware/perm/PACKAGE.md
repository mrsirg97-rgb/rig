# middleware/perm

## What it is

The deny-by-default permission middleware: a static allow-list of tool
names with a second door over the live plugin table (SPEC_PLUGINS 7,
the presence reversal), plus the plugin provenance rule (the model's
write and edit land in plugins/pending/, not plugins/). A denied call
is fed back to the model as a refusal naming the tool and the list,
attributed so downstream guards can bound the repetition.

## What it includes

- `Allowlist(names...)` — permits exactly the listed tool names;
  everything else is denied. This is the nil-door today.
- `AllowlistWithDoor(names, door)` — the allow-list plus a second door
  (the live plugin table's membership): a name the door admits passes
  though absent from the static list. The door speaks for plugins only
  (never a native); a nil door is `Allowlist`.
- `Plugins(pluginsDir)` — the plugin provenance rule (SPEC_SANDBOX 2)
  for `write` and `edit`.
- `resolvedPath` (unexported) — resolves symlinks through the deepest
  existing ancestor before applying the zone boundary.

## How it is consumed

- Both are wired at the root into the middleware chain, `Plugins` listed
  before `Allowlist`: first-listed is innermost, so the allow-list (the
  more basic refusal, the tool's name) is consulted first, and the
  provenance rule speaks only for a call the allow-list has passed.
- `Allowlist` is a wrap-only participant: it adapts through
  `core.ToolMiddlewareFunc` and carries neither optional capability.

## Gotchas

- A denied call returns the same message as both `content` and `error`
  (attributed), so the retry guard can bound the repetition.
- `Plugins` applies only to `write`/`edit`; a malformed `path` arg falls
  through to the tool, as does a target outside the rig home's plugins/
  or inside plugins/pending/ (the forge's landing zone).
- `resolvedPath` prevents an alternate symlink spelling from turning a
  pending path into a live write or hiding a live target behind a foreign
  path. Missing final components remain legal for new pending files.
- The rule guards the honest path, not the boundary: bash can still move
  a file into plugins/ (the operator's shell is the operator's); the
  worker jail is the boundary, the provenance rule is the workflow.
