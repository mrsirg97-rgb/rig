# tool/web

## What it is

The web tools: `web_search` (the local SearXNG instance over net/http) and
`web_fetch` (a guarded HTTP reader with trafilatura extraction and a
stdlib text pass). Stdlib only — no third-party Go client, no new venv.

## What it includes

- `web_search` — a `core.Tool` over the SearXNG `/search` JSON.
- `web_fetch` — a `core.Tool`: resolves the host, refuses private
  addresses (SSRF guard), follows redirects with re-checks and a hop cap,
  extracts via trafilatura or the stdlib text pass.

## How it is consumed

- Registered at the root as native tools; `WebFetchProxy`/`Trafilatura`
  presence-aware settings feed the fetch path.

## Gotchas

- SSRF guard: host resolved before the fetch, private addresses refused
  (v4 private/loopback/broadcast, v6 loopback/ULA/link-local/multicast,
  IPv4-mapped v6 folded), re-checked across redirect hops, hop count
  capped.
- An empty `WebFetchProxy`/`Trafilatura` is a choice (direct egress / the
  stdlib text pass), not an unset.
