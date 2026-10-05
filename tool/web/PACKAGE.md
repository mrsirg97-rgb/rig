# tool/web

## What it is

The one web tool: `web`, action `search` (the local SearXNG instance
over net/http) or `fetch` (a guarded HTTP reader with trafilatura
extraction and a stdlib text pass). Stdlib only; no third-party Go
client, no new venv.

## What it includes

- `Web`: the tool as its interface (2.12.6): `tool.Definition`, `Exec` as
  the one JSON door — it decodes `action`/`target` and the per-action
  optionals, applies the absent-field defaults, and routes, refusing an
  unknown action — and two verbs, `Search(ctx, query, maxResults)` and
  `Fetch(ctx, url, maxChars, timeoutMs)`. The no-query and no-url
  refusals and the `maxResults`/`maxChars`/`timeoutMs` bounds live in the
  verb they belong to, so a Go caller and the model hit the same checks.
  `New(Config)` and `NewDefault()` return the interface; the struct is
  unexported.
- The engines behind it: the SearXNG `/search` JSON call, and the
  guarded reader — resolves the host, refuses private addresses (SSRF
  guard), follows redirects with re-checks and a hop cap, extracts via
  trafilatura or the stdlib text pass.

## How it is consumed

- Registered at the root as one native tool; `WebFetchProxy`/
  `Trafilatura` presence-aware settings feed the fetch engine.
- One allow entry: the permission gate keys on `web`, and approval is
  per tool name, so fetch is allowed with search.

## Gotchas

- SSRF guard: host resolved before the fetch, every address parsed with
  net/netip (unparseable refused, IPv4-mapped v6 unmapped first) and
  refused when loopback, private, unspecified, link-local, multicast,
  0/8, CGNAT 100.64/10, 192.0.0/24, benchmark 198.18/15, TEST-NET
  192.0.2/24, 198.51.100/24 and 203.0.113/24, the 6to4 anycast
  192.88.99/24 or 240/4; re-checked across redirect hops, hop count
  capped.
- Direct egress dials only the addresses the guard vetted for that hop
  (the request ctx carries them; the transport never re-resolves, so a
  DNS rebind between check and dial cannot reach a private listener).
  With a proxy the proxy resolves and dials; the proxy is not guarded.
- The DNS resolution rides the request ctx (the `LookupFn` seam takes
  it): a stalled resolver cannot outlive the fetch's own deadline, and a
  cancelled lookup surfaces the context error.
- The extraction rides the same total budget: `ExtractReadable` takes the
  fetch's ctx, trafilatura's own 20s cap is the floor under the caller's
  deadline, and the subprocess has a one-second `WaitDelay`, so a
  trafilatura child's orphaned grandchildren cannot hold the output
  pipes past the deadline.
- A JSON reply (the content type, or a body that parses) comes back as
  one shape line then the compacted JSON, both under the same `maxChars`
  cap and `[TRUNCATED]` marker: a 77 KB API reply was raw text, and the
  model fell back to curl and jq. The shape names the top-level type,
  its keys, each array's length and its first element's shape
  (`shape: object{count: number, results: array[2] of object{title:
  string}}`); a body that the content type calls JSON but
  that does not parse (a truncated download) falls back to the raw text
  unchanged.
- An empty `WebFetchProxy`/`Trafilatura` is a choice (direct egress / the
  stdlib text pass), not an unset.
- One name means one gate: there is no way to allow `search` while
  refusing `fetch`.
