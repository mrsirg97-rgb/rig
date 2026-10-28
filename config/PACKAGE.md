# config

## What it is

The runtime's config loading (SPEC_CONFIG): the knobs that are today
flags, env vars, and constants become a four-layer resolution with a
user file in it, and the models table leaves code for a file. `config/`
parses; the root (cmd/rig) consumes. Stdlib plus models, nothing else:
no core, no store types (decision 1). JSON only, stdlib encoding/json.

## What it includes

- `Load(dir, cwd)`: reads the user files under `dir` (the rig home)
  and the operator's `AGENTS.md`, each merged over its embedded default,
  and refuses loud when the workspace's project `AGENTS.md` is
  unreadable; returns `*Config`.
- `Config`: `Settings` (`Workers *bool` is the drain pair's tri-state:
  nil = the capability decides, false = off even on a capable machine,
  true = still capability-gated), `Models` (`models.Table`), `Agents`,
  `Theme`, `Notices`. `Notices` carries the once-at-start lines: a present
  `workers.json` (retired, SPEC_WORKERS: the fleet is the resident
  model), a present `concurrency` key in models.json, and the legacy
  `defaultJobModel` — all read, ignored, named.
  `Settings.Plugins` (`SettingsPlugins{Max}`) is the plugin
- `plugins`: `max` caps the door's enum: `enabled` is retired (SPEC_GROWTH 9,
  amended); a non-empty one refuses at load naming the directory switch
  (`plugins/disabled/`), an empty one is dropped.
- `mesh` (3.1.0): the machines' setting, absent by default — no mesh piece
  is constructed and the unix room's wire is 3.0.4's byte for byte. `room`
  names the mesh (the machines that join agree on it; nothing on the wire
  carries it), `listen` is the machine's door (one address the tailnet
  reaches; a wildcard bind refuses), `peers` the other machines' doors,
  `period` the machine clock's cadence in milliseconds (default 250: a
  tailnet round measured 96 ms and the period holds it) and `window` the
  horizon in ticks (default 4: about 1.25 s measured on loopback).
  `iterations` is retired: a file that still carries it is refused by
  name, not ignored.
- `loadSettings` / `parseSettings` / `mergeSettings`: the settings
  chain's file-over-embedded layer. The chain also reports whether the
  file named its own allow; a present `defaultJobModel` is carried to
  `Load`, which names it once and ignores it (SPEC_WORKERS).
- `workersRetired`: the retired `workers.json` read: existence only,
  the content never interpreted; `Load` turns it into the one notice.
- `loadModels` / `parseRows` / `mergeRows`: the model table out of code;
  `concurrency` stays a known row key whose value is read and dropped
  (the retired count), reported up as the notice. The embedded side is
  `[]` (2.12.11): `mergeRows` over it is the operator's table verbatim,
  and the file, the embed, and the merge's error voice stay one path.
- `readAgents`: the operator's AGENTS.md. `ProjectAgents(dir, cwd)`:
  the project's, the nearest file walking up from the workspace to the
  repository root and no further (a non-repo workspace reads its own
  only; the operator's file is skipped when the workspace is the rig
  home); the root reads it at wire so it follows the session's
  workspace (2.11.8).
- `ReadTheme`: the theme.json read (the `/theme custom` load reads it fresh).
- `ReadKey` / `SetKey`: settings.json's two doors (3.5.0, SPEC_WEBTOOLS): a
  key outside `knownSettings` refuses, naming the known ones; the write keeps
  the rest of the document byte-shaped through the same marshal, writes `0600`
  (the file holds the web-bundle password now), and renames over. `ReadKey`
  answers `(value, ok, err)` so an unset key is a fact, not a fault.
- `SetTheme`: the `theme` key's one writer (2.3.2): the file's other
  keys preserved, the write atomic (temp then rename), the value
  verbatim; a malformed existing file refuses in the loader's voice; the
  write itself is `SetKey` (3.5.0) — one writer, one shape.
- The per-key decoders: `jsonString`, `jsonInt`, `jsonFloat`, `jsonAllow`,
  `jsonStringArray`, `gojson`, and the `readErr` voice.

## How it is consumed

- `Load` is called at the root before the store opens: the root applies
  the flag and env layers above the file (decision 2), then resolves the
  active model.
- `Config.Models` is the merged `models.Table` the root uses to resolve
  the active row; `Config.Settings` feeds the flags/env chain.
- `Config.Theme` is the raw `theme.json` document: SPEC_TUI (10) owns the
  palette schema and decodes the raw value.
- `Config.Agents` is the operator's AGENTS.md alone; the project's is a
  wire-time read (`ProjectAgents`).

## Gotchas

- settings.json's `swapUrl` stays parsed and no longer routes the
  instances (3.0.0): a child row's provider sends to the row's port
  directly. The router's rows carry in by hand: the models file's child
  half replaces what the router ran behind. The scheduler still reads
  the swap for its own worker site: the resident model it resolves and
  the `-base-url` its workers carry come from the swap the setting
  names.
- Absent files are silent (3): present-but-malformed or unreadable files
  refuse loud, naming the file and, for JSON, the field (3).
- The read order is fixed: the first malformed file wins,
  deterministically: settings.json, models.json, workers.json,
  theme.json, AGENTS.md (global, then project). `Load` never creates a
  file.
- The embedded allow is the native set (12 names) grown by `scheduler`
  and `delegate` whenever the operator named no allow of their own
  (their list stands as written) — the worker tools register for every
  run now (SPEC_WORKERS). The embedded models table carries no rows: the
  table is the operator's file, and a named model the file does not
  define is the root's refusal at start (`models.Resolve`'s voice, before
  any store opens), as a nameless run has been since 0.25.6. `/models`
  lists what the operator wrote, in `Known()` order, and nothing else.
- The settings chain is embedded < file, with the flag/env layers applied
  by the root above; zero means unset at the file layer (an empty string
  or zero descends), except the two presence-aware keys
  (`webFetchProxy`, `trafilatura`), for which present is set; even empty
  (2, 5).
- Model rows: the embedded table is empty (2.12.11), so the operator's
  file is the table and a row with only `id` and `window` refuses for its
  numbers. The merge keeps its shape for a non-empty embedded side (a
  user row overlays per-field on an embedded id, each set field replacing
  and each unset keeping; a new id requires its numbers and takes the
  defaults role interactive, effort ""; unlisted embedded rows are kept),
  and its cases run on a literal base row in `modelsmerge_test.go`.
  The hosted keys (SPEC_HOSTED 1) merge the same way:
  `remote`, `provider` (a name implies remote), `baseUrl`, `apiKey`
  (never rendered by `/models`), `reasoning`,
  `providerPin` (a string or an array), `cacheControl`, `retries`. A
  duplicate id in the same file refuses at the second
  occurrence.
- The overlay's zero-means-unset has one named cost: a zero numeric value
  is unreachable by overlay on a table id.
- A post-merge invariant error names the row id and the clause (the
  `models.Check` voice), with the file (3).
- `readAgents` concatenates global-first with a blank line between,
  empty segments skipped, the content as written (no markers, no
  headers, no indentation); ENOENT is silent, every other read error
  refuses with the OS reason, the path named once.
- `readTheme` validates well-formedness only: no fields, no keys, no
  schema: the moment it named a field it would own 10's territory.
- `sandbox` must be `"jailed"`, `"landlock"`, or `"off"` (SPEC_SANDBOX 5, 6): landlock is the kernel LSM profile for boxes that cannot run the bwrap jail.
- `approve` is the approval dial's default (SPEC_MODES 4): `auto` or
  `manual`; anything else refuses at load.
- `updateKey` is the pinned minisign public key that signs releases
  (SPEC_BUILD 5): the `-update` path's authenticity, `RIG_UPDATE_KEY`
  above it, a file value over it, then the embedded `settings.json` —
  the embedded default pins the operator's release key; a build whose
  embedded key is empty fails closed (an unpinned update refuses).
