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
- `Definition`: the interface a tool embeds to satisfy three of
  `core.Tool`'s four methods: `Name()`, `Description()` (composed as
  `<what> guidelines: <guidelines> reply: <reply>`), `Schema()` (a copy of
  the raw object, key order preserved), and `Enabled()`. Since 2.9.5 it
  is an interface, not a struct: the registry's entry is the one
  concrete behind it, unexported, and nothing outside the package names
  it; a tool holds the abstraction and a test can hand it any
  Definition.
- `Def(name)`: the entry by name, as the interface; an unknown name
  panics, since a tool asking for words it does not have is a
  programmer error caught at start, never at the model's call.
- `Fill(d, slot, value)`: a Definition wrapping another with a slot
  replaced in the description and the schema, for the one tool whose
  text carries a runtime value (`scheduler` names the default model as
  `{default_model}` in its schema; `delegate` did too until 2.10.1
  reworded it, and its wrap went in 2.11.0); delegation by embedding,
  the registry's own copy never changes.
- `Names()`: the enabled names in file order; `AllNames()`: every entry.

## How it is consumed

- Each tool package embeds `tool.Definition` in its tool type and writes
  only `Exec`; the one with live text wraps its own with `Fill`; `verdict`
  and `decide` are conditional natives, registered only when their door
  exists (a fleet pipe, a decision server). `plugin`
  implements `Schema()` itself to add the live name enum to the
  registry's schema: the embedded interface supplies the rest.
- `cmd/rig` derives the native tool list from `Names()`, so flipping
  `enabled` to false removes a tool from the build's menu without
  deleting its words.

## Gotchas

- The schema is kept as raw bytes so key order survives to the wire; the
  wire compacts it, so whitespace in the file is free.
- A new tool is a registry entry first; `cmd/rig`'s registry test fails
  when a wired tool has no entry or an entry has no wiring.
- A tool's description drifts with its state: the more verbs a tool
  carries, the further its entry lags the behavior. A behavior change to
  a wired tool diffs its registry entry in the same change; the wire
  prefix golden moves with it, deliberately.
