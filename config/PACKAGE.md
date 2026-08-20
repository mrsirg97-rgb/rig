# config

## What it is

The runtime's config loading (SPEC_CONFIG): the knobs that are today
flags, env vars, and constants become a four-layer resolution with a
user file in it, and the models table leaves code for a file. `config/`
parses; the root (cmd/rig) consumes. Stdlib plus models, nothing else —
no core, no store types (decision 1). JSON only, stdlib encoding/json.

## What it includes

- `Load(dir, cwd)` — reads the user files under `dir` (the rig home)
  and the AGENTS.md pair (`dir` + `cwd`), each merged over its embedded
  default; returns `*Config`.
- `Config` — `Settings`, `Models` (`models.Table`), `Agents`, `Theme`.
- `loadSettings` / `parseSettings` / `mergeSettings` — the settings
  chain's file-over-embedded layer.
- `loadModels` / `parseRows` / `mergeRows` — the model table out of code.
- `readAgents` — the AGENTS.md pair.
- `readTheme` — the theme.json read.
- The per-key decoders: `jsonString`, `jsonInt`, `jsonAllow`,
  `jsonStringArray`, `gojson`, and the `readErr` voice.

## How it is consumed

- `Load` is called at the root before the store opens; the root applies
  the flag and env layers above the file (decision 2), then resolves the
  active model.
- `Config.Models` is the merged `models.Table` the root uses to resolve
  the active row; `Config.Settings` feeds the flags/env chain.
- `Config.Theme` is the raw `theme.json` document; SPEC_TUI (10) owns the
  palette schema and decodes the raw value.
- `Config.Agents` is the assembled global-then-project AGENTS.md.

## Gotchas

- Absent files are silent (3); present-but-malformed or unreadable files
  refuse loud, naming the file and, for JSON, the field (3).
- The read order is fixed — the first malformed file wins,
  deterministically: settings.json, models.json, theme.json, AGENTS.md
  (global, then project). `Load` never creates a file.
- The settings chain is embedded < file, with the flag/env layers applied
  by the root above; zero means unset at the file layer (an empty string
  or zero descends), except the two presence-aware keys
  (`webFetchProxy`, `trafilatura`), for which present is set — even empty
  (2, 5).
- Model rows: a user row overlays per-field on an embedded id (each set
  field replaces, each unset keeps); a new id requires its numbers and
  takes the defaults (role interactive, effort ""); unlisted embedded
  rows are kept. A duplicate id in the same file refuses at the second
  occurrence.
- The overlay's zero-means-unset has one named cost: a zero numeric value
  is unreachable by overlay on a table id.
- A post-merge invariant error names the row id and the clause (the
  `models.Check` voice), with the file (3).
- `readAgents` concatenates global-first with a blank line between,
  empty segments skipped, the content as written (no markers, no
  headers, no indentation); ENOENT is silent, every other read error
  refuses with the OS reason, the path named once.
- `readTheme` validates well-formedness only — no fields, no keys, no
  schema: the moment it named a field it would own 10's territory.
- `sandbox` must be `"jailed"` or `"off"` (SPEC_SANDBOX 5).
