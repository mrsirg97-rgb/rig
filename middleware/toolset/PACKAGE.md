# middleware/toolset

## What it is

The root's live tool list (SPEC_PLUGINS 8): the kernel's tools as a
per-turn fact, not a startup snapshot. The loop borrows two things per
turn — the provider (the request's tools array) and the execution chain
(the middleware) — and the root owns both ends of this table. A swap is
one atomic write: the next turn's request carries the new list and the
new tools execute, by construction — the models-switch's semantics
(SPEC_COMMANDS 6), zero loop lines. Stdlib plus core, nothing else.

## What it includes

- `Table` — the live list: ordered (the wire's order), swapped
  atomically under a `sync.RWMutex`, plus the plugin subset.
- `New(tools...)` — builds the table, copying the caller's slice.
- `Set(tools)` — swaps the list (the reload's rebuild), copy-in.
- `SetPlugins(names...)` — marks the currently-live plugin names (the
  reload carries its own plugin subset); `IsPlugin` answers the table's
  plugin membership.
- `List()` — the table's tools as a snapshot.
- `Specs()` — the wire's projection: name, description, schema per
  tool, in the table's order.
- `NativeSpecs()` — the request's list (SPEC_GROWTH 9): the natives
  plus the plugin door, never the per-plugin schemas; `Carry` stamps
  this, not `Specs()`.
- `PluginNames()` — the live plugin names, sorted (the door's schema
  enum, deterministic).
- `Tool(name)` — the door's lookup seam: the named tool, if the table
  carries it.
- `Resolve(t)` — the exec's end middleware.
- `Carry(t, inner)` — the request's end provider wrapper.

## How it is consumed

- `New` at the root builds the table from native + plugin tools.
- `Resolve` is listed first (innermost, first-listed is innermost) in the
  middleware chain, so the chain's participants (the allow-list, the
  bound) still bound what the table serves.
- `Carry` wraps the provider (compact's decorator); it stamps the table's
  `NativeSpecs()` into the request's tools array before delegating, per
  call — the natives plus the door, the plugin schemas behind
  `plugin`'s `schema` arm (SPEC_GROWTH 9).
- `IsPlugin` feeds the allow-list's second door (`perm.AllowlistWithDoor`,
  SPEC_PLUGINS 7): a name the table carries as a plugin passes the
  allow-list though absent from the static list.
- The loop's own startup list is a bootstrap for the chain's lookup.

## Gotchas

- `New` and `Set` copy-in: the caller's slice is not retained; a later
  mutation of the caller's slice does not reach the table.
- A reader sees one list or the next, never a mix (the RWMutex swap).
- `Resolve` falls through to the inner exec for a name the table does not
  carry (the loop's directExec, whose unknown-tool voice bounds the rest).
- `Carry` stamps per call: a swap takes effect on the next call; an
  in-flight request keeps the list it was stamped with.
- `List`/`Specs`/`tool` snapshot under read-lock; the exec path holds the
  read lock only for the lookup, not across the tool call.
