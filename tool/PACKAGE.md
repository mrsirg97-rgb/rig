# tool

## What it is

The registry of the model's words: `registry.json`, embedded, is the one
file holding every native tool's description and schema (SPEC_CORE's
house shape made data: `what`, `guidelines`, `reply`, and the schema
object as it goes on the wire). Stdlib only (`embed`, `encoding/json`,
`strings`); a leaf every tool package imports.

## What it includes

- `registry.json`: one entry per native tool: `name`, `enabled`, `what`,
  `guidelines`, `reply`, `schema`. File order is menu order for the root.
- `Definition`: the value a tool embeds to satisfy three of `core.Tool`'s
  four methods: `Name()`, `Description()` (composed as
  `<what> Guidelines: <guidelines> Reply: <reply>`), `Schema()` (a copy of
  the raw object, key order preserved). `Enabled()` reads the flag.
- `Def(name)`: the entry by name; an unknown name panics, since a tool
  asking for words it does not have is a programmer error caught at
  start, never at the model's call.
- `Fill(slot, value)`: a copy with a slot replaced in the words and the
  schema, for the two tools whose text carries a runtime value
  (`scheduler` and `delegate` name the default model as
  `{default_model}`); the registry's own copy is never changed.
- `Names()`: the enabled names in file order; `AllNames()`: every entry.

## How it is consumed

- Each tool package embeds `tool.Definition` in its tool type and writes
  only `Exec`; the two with live text override one method. `plugin`
  overrides `Schema()` to add the live name enum to the registry's
  schema.
- `cmd/rig` derives the native tool list from `Names()`, so flipping
  `enabled` to false removes a tool from the build's menu without
  deleting its words.

## Gotchas

- The schema is kept as raw bytes so key order survives to the wire; the
  wire compacts it, so whitespace in the file is free.
- A new tool is a registry entry first; `cmd/rig`'s registry test fails
  when a wired tool has no entry or an entry has no wiring.
