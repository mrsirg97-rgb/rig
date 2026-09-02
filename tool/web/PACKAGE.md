# tool/web

## What it is

The web tools: `web_search` (the local SearXNG instance over net/http) and
`web_fetch` (a guarded HTTP reader with trafilatura extraction and a
stdlib text pass). Stdlib only; no third-party Go client, no new venv.

## What it includes

- `web_search`: a `core.Tool` over the SearXNG `/search` JSON.
- `web_fetch`: a `core.Tool`: resolves the host, refuses private
  addresses (SSRF guard), follows redirects with re-checks and a hop cap,
  extracts via trafilatura or the stdlib text pass.

## How it is consumed

- Registered at the root as native tools: `WebFetchProxy`/`Trafilatura`
  presence-aware settings feed the fetch path.

## Gotchas

- SSRF guard: host resolved before the fetch, every address parsed with
  net/netip (unparseable refused, IPv4-mapped v6 unmapped first) and
  refused when loopback, private, unspecified, link-local, multicast,
  0/8, CGNAT 100.64/10, 192.0.0/24, benchmark 198.18/15 or 240/4;
  re-checked across redirect hops, hop count capped.
- Direct egress dials only the addresses the guard vetted for that hop
  (the request ctx carries them; the transport never re-resolves, so a
  DNS rebind between check and dial cannot reach a private listener).
  With a proxy the proxy resolves and dials; the proxy is not guarded.
- An empty `WebFetchProxy`/`Trafilatura` is a choice (direct egress / the
  stdlib text pass), not an unset.
