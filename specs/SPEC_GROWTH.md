# rig: the growth surface — pending plugins, scheduled jobs, and the plugin door (pre-1.0)

The plugins are live and the count is growing (0.8.2, 20 loaded). This
spec is the next surface, written while the field test is on the daily
driver: three pending plugins that read the shared business (the
5090watch project: the GPU tracker and the crypto range trade), one pure
calculator, a scheduled-job menu, and the harness amendment that keeps a
grown plugin table from blowing the request context. It is the first
harness change SPEC_PLUGINS 7 named as "a later decision once the count
grows" — the count has grown; this is that decision.

The baseline is 0.8.2 (main at the plugins land). The fixture runs (no
plugins directory) keep the 0.2.0 wire byte-exact: the natives' specs
are unchanged and the door is a native whose schema is small and known;
the golden fixtures regenerate in place only if the native set itself
changes.

## goals

- **The creative set, as pending plugins** (the operator's review is the
  gate): three read-only probes over the shared business's own files
  (`networth`, `deal_pulse`, `btc_signal`) and one pure calculator
  (`flip_calc`). Each is a plugin file under `plugins/pending/`, the
  existing contract (DESCRIPTION, SCHEMA, `run(args) -> str`), discovered
  only after the operator's `/plugins approve`.
- **A scheduled-job menu** the worker (a headless `rig -p`) can run
  against the plugins it sees in its own home: the daily digest
  (scheduled now) and the weekly security sweep (proposed).
- **The plugin door + enable/disable** (the harness amendment): one
  `plugin` tool collapses all plugin schemas to one request entry, so a
  grown table stops blowing context; `plugin_schema` fetches one plugin's
  spec on demand; `/plugins enable|disable` is the hide/turn-off surface.

## non-goals

- No new plugin contract: the pending files are the existing one-file,
  one-tool shape, three names.
- No new stores: enablement rides the rig home's `settings.json` (the
  config layer, SPEC_CONFIG), not a new schema.
- No new scheduler machinery: the digest and the sweep are the existing
  `scheduler` + `rig -p` path (SPEC_SCHEDULER), composed prompts.
- The door does not nest plugins as sub-verbs of one mega-tool: the
  plugins stay real `core.Tool`s in the table, callable by the python
  tool and by name; the door is a dispatch convenience, not a new shape.
- No automatic promotion, no manifest, no versioning: unchanged.

## layout

```
plugins/pending/        the four creative files, written by this spec:
  networth.py           the shared business's money standing (read-only)
  deal_pulse.py         the GPU-deal state (read-only)
  flip_calc.py          the pure flip-profitability arithmetic
  btc_signal.py         live price vs the trade triggers (one bounded call)

specs/
  SPEC_GROWTH.md        this file
  SPEC_PLUGINS.md       9: the door and the enablement (the amend this
                        spec rides); 7's "later decision" named
docs/
  SETUP.md, USAGE.md, CHANGELOG.md, ROADMAP.md   the surface follows
middleware/toolset/
  toolset.go            +NativeSpecs, +PluginNames, +Get; Carry stamps
                        natives + the door (9)
plugins/
  plugins.go            +the plugin door's Exec (a native over the
                        table), +PluginNames/PluginSchema surfaces
command/
  plugins.go            the enable|disable verbs (9)
cmd/rig/
  main.go               the door's registration, the enabled-set wiring,
                        the /plugins env, Version 0.9.0
```

## the pending plugin set

The files land in `plugins/pending/` — invisible to discovery (the
listing is top-level `*.py`), refused by the allow-list until the
operator's `/plugins approve` moves them up (SPEC_SANDBOX 2). The
operator reviews the spec here and the files there, then blesses.

### networth

The shared business's money standing, read-only over the project's own
files. `wallet.json` (the seed's public key) and `crypto-state.json`
(the lane's own updated ledger: `sol_balance`, `btc_held`, `farm_msol`,
`last_action`) — the balances are the lane's on-chain ledger, no RPC.
The USD values need live prices (SOL and mSOL are not the state's
`btc_price`), so the plugin makes one bounded call to CoinGecko's
simple-price for `solana,bitcoin,msol`; fail-closed, a fetch error still
reports the balances with no USD values, named. The mSOL farm's USD is
its own live price (the staking yield accrues; never invent a rate).
Used by me to report the business to the operator and to decide, bounded.

### deal_pulse

The GPU-deal state, read-only over `offers.json` (the snapshot),
`state.json` (the alerts), and `config.json` (the thresholds). Reports:
offer count, the cheapest in-stock 5090 by price with retailer and URL,
the watchlist items' price and stock, the alert threshold, the last
alert. Used to decide whether to flip or post.

### flip_calc

Pure arithmetic: buy price, sell price, optional per-source fees,
shipping, tax rate, quantity → gross, total cost, net profit, ROI, and
the breakeven sell price. Zero network, deterministic, bounded. The
side-hustle calculator I reach for when a deal_pulse says a card is
cheap.

### btc_signal

Live BTC price, one bounded call (CoinGecko's simple-price endpoint,
timeout, fail-closed: a fetch error still shows the triggers and the
state's last price, named stale), compared to the range bot's triggers
(≤ 63,000 buy / ≥ 65,500 sell) and the perp entry (≤ 63,000 dip or ≥
65,500 breakout, OPS.md §4). Answers "where are we" instantly, between
the cron runs.

## the scheduled-job menu

The jobs run the worker (a headless `rig -p` session, SPEC_SCHEDULER)
with a composed prompt; the worker sees the approved plugins in its own
home and calls them as tools. Reports land through rem (scoped to the
cwd) and `notify`.

- **daily digest** (scheduled now, ~07:30): GPU health, system health,
  networth, deal_pulse — the morning report. Bounded, read-only, one
  worker slot.
- **weekly security sweep** (proposed): Monday morning, `cron_audit`,
  `secret_scan`, `listen`/`net_conn` drift, `cert_check` on the operator's
  domains — the walls' weekly look. Not scheduled: the operator reviews
  the menu before more background work commits.

## decisions

### 9. The door and the enablement (SPEC_PLUGINS 7's "later decision")

**The count has grown; nesting is the named decision.** Decision 7 said
"Plugins stay flat as real tools; nesting them under one tool is a later
decision once the count grows." The count has grown (20). The flat shape
is the problem: `toolset.Carry` stamps every tool's full spec into every
request, and a grown plugin table is twenty schemas the model reads
before it can act. The fix is a **door**, not nesting: the plugins stay
real `core.Tool`s in the table, and the request carries natives plus one
`plugin` door.

**The door.** A new native tool `plugin`, schema `{"name": string, "args":
object}`: `Exec` resolves the named plugin in the live table and calls
it. An unknown name is a loud tool error naming the live plugins. The
`name` field's `enum` is the live plugin names (the swap's own list) —
the model sees what is callable, cheap, no per-plugin schemas. A second
native `plugin_schema`, schema `{"name": string}`, returns one plugin's
description and schema verbatim: the model fetches the args it needs
when it calls a non-trivial plugin. The two doors' schemas are small and
known; the request drops twenty large plugin schemas for two small ones.
The door's descriptions are the operator's, stated once in the schema:
"plugins run via the `plugin` door; `plugin_schema <name>` shows a
plugin's contract."

**Carry stamps natives + the door.** `toolset.Table` gains
`NativeSpecs()` (the non-plugin tools, as today) and `PluginNames()`;
`Carry` stamps `NativeSpecs()` plus the door's two specs into every
request. A plugin the swap adds is callable by the door the next turn
(the name's in the door's enum, by construction); the python tool still
reaches it by `import` (the shared namespace, unchanged).

**Enablement is the hide/turn-off surface.** `settings.json` gains a
`plugins` object (the config layer, SPEC_CONFIG): `{"enabled": ["networth",
...], "max": 8}` — a disabled name is not wired as a tool (the door's
enum carries enabled plugins only), so it is hidden entirely, not
callable. `max` caps the door's enum at the top `max` live plugins in
file order (the deterministic order, decision 2) — the "show a certain
number" knob. The root reads `enabled`/`max` at wiring and applies the
cap to the table; `/plugins enable <name>` / `/plugins disable <name>`
(decision 8's verbs, the same door as the reload) toggle a name in the
file and reload — the models-switch semantics, next-turn, exactly.

**The costs, named.** A reload at deep context is still one full
re-prefill (decision 8, unchanged). The door's enum is a per-request
re-read of the table (the swap's atomicity, unchanged). A model that
calls `plugin` with a disabled name gets the loud unknown-name voice —
the disable is total, never a soft miss.

**Rejected, named.** A per-plugin toggle tool the model drives: the
enablement is the operator's (SPEC_SANDBOX 2's ownership), and a
capability the model switches on itself is the auto-promotion shape
decision 8 rejected. A purely-freeform `args` with no schema door: the
model needs the args shape to call `flip_calc` correctly, and
`plugin_schema` is the cheap answer. A hard cap that drops plugins
without a door: an invisible tool is uncallable; the door keeps every
enabled plugin reachable.

## testing

Named cases, failing first. The pending plugins are reviewed by eye and
executed against the real files (no unit harness — they are python, the
kernel is the seam). The door and the enablement are Go, tested in the
existing leaves.

**toolset (the seam, pure core):**

- `TestNativeSpecsExcludesPlugins` — the table with a native and a
  plugin: `NativeSpecs()` carries the native's spec only; `PluginNames()`
  carries the plugin's name; a swap updates both.
- `TestGetResolvesTheTable` — `Get` returns the table's tool for a
  live name and nil for an absent one (the door's exec).

**plugins (the leaf, fake kernel — no python required):**

- `TestDoorSurfacesAreTheNativeContract` — `plugin` and `plugin_schema`
  are natives: Name, the small schemas, the descriptions naming the
  doors; `plugin`'s schema carries the live names' enum.
- `TestDoorExecResolvesAndCalls` — `plugin` with `{name, args}` calls
  the named plugin's Exec (args verbatim, result verbatim); an unknown
  name is a loud tool error naming the live plugins; `plugin_schema`
  returns the named plugin's description and schema verbatim.

**command (the leaf, fakes at the Env seam):**

- `TestPluginsEnableDisableToggleTheFileAndReload` — the enable verb
  adds a name, the disable verb removes it, the file writes, the reload
  seam's reply rides through; a nil seam refuses with the no-seam voice.

**cmd/rig (root + e2e):**

- `TestDoorWireStampsNativesPlusTheDoor` — the request's tools array is
  the natives plus `plugin` and `plugin_schema` (no per-plugin schemas);
  the door's `name` enum carries the live plugin names.
- `TestEnablementHidesAndCaps` — `settings.json` `plugins.enabled` drops
  a plugin from the door's enum (not callable, loud unknown); `max` caps
  the enum at the top names in file order; the python tool still imports
  the enabled plugins (the shared namespace).
- `TestDoorRoundTripsNextTurn` — a reload's swap adds a plugin; the next
  turn's request carries it in the door's enum and `plugin` executes it;
  the natives keep executing.

The suite is green on a box with no model and no python: the leaf cases
are fake-kernel, the e2e cases skip cleanly without the kernel gate, the
golden fixtures are untouched (the door is a native with a small known
schema; the no-plugins wire is natives + the two doors).

## the diffs this spec implies

- **SPEC_PLUGINS**: decision 9 (the door and the enablement) — the amend
  this spec rides; the goals, the non-goals, decision 7's cross-reference.
- **SPEC_CONFIG**: `settings.json` gains the `plugins` object (`enabled`,
  `max`) — the enablement's config layer.
- **`middleware/toolset`**: `NativeSpecs`, `PluginNames`, `Get`;
  `Carry` stamps natives + the door.
- **`plugins`**: the door's `Exec` and `PluginSchema`, the `PluginNames`
  surface the door's enum reads.
- **`command`**: the `enable <name>` / `disable <name>` verbs (8's door).
- **`cmd/rig`**: the door's registration, the enabled-set wiring, the
  `plugins` env, Version 0.9.0.
- **CHANGELOG + Version**: `0.8.2` → `0.9.0`.

## scope

What this is not:

- The creative plugins are pending, not live: the operator's `/plugins
  approve` is the gate, and this spec's review is the operator's
  checklist.
- The scheduled jobs are the existing scheduler path, composed — no new
  machinery; the sweep is proposed, not scheduled.
- The door is not a sandbox, not a manifest, not a dependency story:
  unchanged.
