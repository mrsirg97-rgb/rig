# tool/web: the one web tool (search and fetch)

One leaf package, one tool: pane's web_search and web_fetch, ported,
then folded in 1.7.3 behind the single `web` name. Search talks to the
local SearXNG instance (the ~/docker/web-tools compose, :8888); fetch
is a guarded HTTP reader (DNS re-check, redirect re-guard, byte/char
caps, egress proxy through the compose's tinyproxy :8889) with HTML
extraction via trafilatura and a stdlib fallback. Stdlib only: net/http,
net, os/exec; no third-party Go client.

## goals

- One tool, one name: `web`, action `search` or `fetch`, on one `target`
  field (the query, or the URL).
- search: SearXNG JSON over net/http: endpoint from env
  (RIG_SEARXNG_URL, default pane's http://127.0.0.1:8888); results
  mapped to title/url/snippet with the 300-char snippet cap and the
  maxResults slice (1..20, default 5), loud `no results for "<query>"`.
- fetch: pane's guarded fetch verbatim: http(s) only, DNS resolution
  refuses private and link-local space with a readable error, every
  redirect hop re-checked, hop cap, textual content types only, declared
  Content-Length and streaming byte cap (5 MiB) with loud truncation,
  20 000-char cap with the named elision marker, 30 s default timeout,
  optional egress proxy from env (default http://127.0.0.1:8889, the
  web-tools compose's tinyproxy) with the unreachable-proxy fix-it voice.
- Extraction: trafilatura as a documented external (pane's own mechanism),
  resolved from the shared agent venv then PATH, overridable by
  RIG_TRAFILATURA; absent or failing degrades to pane's stdlib text
  pass and says so in the content. Extraction rides the fetch's own
  deadline: the subprocess gets the remaining budget (its 20 s cap is
  the floor), and a one-second WaitDelay keeps an orphaned grandchild
  from holding the output pipes past it.
- The surface is one `core.Tool`: the description carries the two
  one-line clauses (search "<query>", fetch <url>) and folds both
  guidelines into one paragraph; the schema is hand-written:
  `required: ["action", "target"]`, action enum `[search, fetch]`, the
  integer bounds (maxResults 1..20; maxChars min 100; timeoutMs min
  1000). Every runtime voice is pane's verbatim.

## non-goals

- No new dependencies: `go.mod` unchanged (net/http + os/exec are
  stdlib).
- No loop change: no new events, no middleware, no hooks.
- No caching, no cookie jar, no TLS pinning, no streaming to the model:
  fetch returns one capped text blob, as pane's does.
- No bundled trafilatura: no venv rig owns, no pip install. Extraction
  is a soft dependency and degrades loudly instead of bootstrapping.
- No live-network tests: every case runs against httptest servers and
  injected seams; the suite is green on a box with no SearXNG and no
  trafilatura.
- No search-side guard: SearXNG is loopback by design (127.0.0.1): the
  fetch engine carries the guard.
- No split approvals: one tool name means one allow entry; the gate
  cannot allow search while refusing fetch (approval is per tool name).

## layout

```
tool/web/
  web.go      the one tool: Config/New/Web, the dispatch, the schema
  search.go   the SearXNG engine: the call, the result mapping
  fetch.go    the guarded fetch engine: the guard, extraction, caps
  web_test.go pane's named cases in pane's order + the fold cases
```

`core/`, `loop/`, `middleware/`, `policy/`, `provider/`: untouched.

## interfaces

One `core.Tool`, the engines behind it:

```go
// web.go; the concrete type unexported with the named constructor,
// the core/tool.go house shape
const DefaultSearXNG = "http://127.0.0.1:8888" // pane's PI_SEARXNG_URL default
const DefaultProxy   = "http://127.0.0.1:8889" // pane's PI_WEB_FETCH_PROXY default
type Config struct {
    Search SearchConfig   // BaseURL (pane appends /search), Do seam
    Fetch  FetchConfig    // Proxy, Trafilatura, Lookup, Do, MaxBytes
}
type web struct{ search *search; fetch *fetch }
func New(cfg Config) *web          // the injection seam (pane's Deps)
func Web() *web                    // the defaults: SearXNG default, proxy on
func (w *web) Name() string        // "web"
func (w *web) Description() string // search line + fetch line, then the guidelines
func (w *web) Schema() json.RawMessage
func (w *web) Exec(ctx context.Context, args json.RawMessage) (string, error)
    // unmarshal action/target + optionals; validate at the boundary;
    // dispatch to w.search.exec or w.fetch.exec

// search.go: the engine, pane's functions verbatim
type SearchConfig struct{ BaseURL string; Do func(*http.Request) (*http.Response, error) }
func NewSearch(cfg SearchConfig) *search
func (s *search) exec(ctx, query string, maxResults int) (string, error)

// fetch.go: the engine
const (
    maxBytesDefault  = 5 * 1024 * 1024         // pane's MAX_BYTES
    maxChars         = 20_000                  // pane's MAX_CHARS
    defaultTimeoutMs = 30_000                  // pane's DEFAULT_TIMEOUT_MS
    maxHops          = 5                       // pane's MAX_HOPS
)
type FetchConfig struct {
    Proxy       string        // egress proxy; "" = direct
    Trafilatura *string       // pane's string|null|undefined: nil = default resolution, &"" = off, &s = that binary
    Lookup      func(context.Context, string) ([]string, error) // DNS seam; nil = system resolver; the request ctx (amended 0.24.6)
    Do          func(*http.Request) (*http.Response, error) // transport seam
    MaxBytes    int           // 0 = the default
}
type Fetched struct{ FinalURL string; Status int; ContentType string; Body string; BodyTruncated bool } // pane's Fetched
type fetch struct{ /* config, resolved trafilatura */ }
func NewFetch(cfg FetchConfig) *fetch
func (f *fetch) Guarded(ctx context.Context, raw string) (Fetched, error) // pane's fetchGuarded
func (f *fetch) exec(ctx, raw string, maxC, timeoutMs int) (string, error)

// shared surface, pane's functions verbatim
func IPisPrivate(ip string) bool            // pane's ipIsPrivate over net/netip, same refusal set as a superset
func HtmlToText(html string) string         // pane's htmlToText (the RE2 port)
func CapChars(text string, max int) string  // pane's capChars
func ExtractReadable(html string, trafilatura *string) (string, string) // + the rig announcement footer
func DefaultTrafilatura() string            // shared venv -> PATH, "" when absent
```

The dispatch owns the boundary voices: bad args, a missing target,
out-of-range bounds, and an unknown action read `web: ...`; the engine
voices (the SearXNG status, the guard refusals, the proxy fix-it, the
truncation markers) are unchanged.

## decisions

- **One name, one approval.** web_search and web_fetch were two tools
  and two allow entries for one capability family. The wire slot, the
  approval gate, and the system prompt all key on tool name, and the
  model was being asked to hold two names for one leaf package. The
  pair folds into `web`; the allow list keys on it, and approval being
  per tool name means fetch is allowed with search. The engines stay:
  two files, one dispatch, no loop change.
- **Action first.** `action` is required and validated at the boundary
  (an unknown action refuses, naming the two values), and `target` is
  the one content field — the query for search, the URL for fetch — so
  the schema cannot ask the model to fill two differently-named fields.
  The per-action optionals ride one flat object; the other action's
  optionals are ignored (a search carrying maxChars still searches).
- **Extraction: the documented external, not a bundled dependency.**
  trafilatura is a soft dependency; without it the tool still works
  (pane's htmlToText is a real path, not a stub), so it degrades loudly
  instead of bootstrapping. The python kernel's venv exists because the
  kernel is a hard dependency with no fallback; installing trafilatura
  would add a second venv (or grow pane's), a pip-install side effect on
  the fetch path, and a second attack surface, to buy extraction
  *quality*. Resolution: the shared agent venv's
  `~/.pi/agent/kernel-venv/bin/trafilatura` first (interop with pane,
  the same venv the python kernel prefers), then `trafilatura` on PATH.
  `RIG_TRAFILATURA` is the operator's explicit choice (a path, or
  empty to disable); the RIG_PYTHON pattern. Pane's
  `string | null | undefined` opts map onto `*string` exactly: nil =
  default resolution, non-nil = explicit (empty = off).
- **The fallback says so (rig over pane).** pane falls back to
  htmlToText silently; rig appends a named footer
  (`[trafilatura unavailable; stdlib text pass used]` and the
  failed/empty variants) because a quiet quality change is exactly the
  kind of silent behaviour the house rules refuse. The success path
  stays word-for-word pane's.
- **RE2, not backreferences.** pane's script/style stripper uses a `\1`
  backreference; Go's regexp has none. The five block types become five
  non-greedy patterns (equivalent for matched pairs, the only sane HTML);
  the named case exercises the same input.
- **The guard is check-and-pin, per hop.** resolve, parse every
  address with net/netip (unmap 4-in-6, refuse unparseable, loopback,
  private, unspecified, link-local, multicast and the reserved v4
  blocks: 0/8, 100.64/10, 192.0.0/24, 198.18/15, TEST-NET
  192.0.2/24, 198.51.100/24 and 203.0.113/24, the 6to4 anycast
  192.88.99/24, 240/4), then dial only
  the vetted addresses: the request ctx carries them and the direct
  transport's DialContext connects to those, never re-resolving the
  host, so a DNS rebind between check and dial cannot land on a
  private listener. The Location of every redirect hop re-runs the
  same guardedUrl against the previous URL as base and re-pins before
  the next fetch. A redirect into private space is refused with the
  same voice as the first hop (pane's named case). The proxy is not
  guarded and not pinned: it is loopback by construction, resolves the
  host itself, and guarding it would need a second policy.
- **Timeout is the whole fetch, all hops included** (amended 0.24.6:
  the DNS resolution rides the same ctx, so a stalled resolver cannot
  outlive the deadline): pane's signal is created once outside the
  loop; Go's ctx carries the same shape (WithTimeout over the
  fetchGuarded call, and the seam's lookup now takes it). A cancelled
  caller ctx returns the context error; the timeout voice names the ms
  and the current URL.
- **Byte cap is declared-then-streamed.** a Content-Length above the cap
  refuses before any download (pane's named case); otherwise the stream
  is read to the cap and the truncation is named in the content
  (`[TRUNCATED: download hit the byte cap; content is partial.]`), with
  the char cap applied after extraction and its own louder marker.
- **Voices are pane's verbatim**, including the fix-it: an unreachable
  proxy reads `egress proxy <url> is unreachable. Start it: cd
  ~/docker/web-tools && docker compose up -d`. The search error is
  `SearXNG search failed: HTTP <status>`. One named port difference:
  a refused connection is Go's voice (`dial tcp ...: connect:
  connection refused`), not Node's ECONNREFUSED; the named case asserts
  the loud shape, not the OS string.
- **Query strings are built by hand**, in pane's order
  (`?q=<escaped>&format=json`), not url.Values (which would sort the
  keys); the named case asserts pane's exact URL.
- **The guidelines teach both shapes** (1.2.12, folded 1.7.3). The live
  SearXNG serves navigational and dictionary junk for brand-heavy
  queries from whichever engine answers, and every session relearned
  the failure mode by burning queries on it. The one paragraph now
  opens with the pair's split (`search finds, fetch reads — snippets
  are not the page; web pages and textual APIs only`), then the
  Search: head (natural-language multi-word queries, refuse leading
  brand/single-token shapes, route known URLs to fetch, demand a
  reword rather than an identical retry on junk, compact JSON reply)
  and the Fetch: head (the capped text reply, the named elision
  marker, private and internal addresses refused). Prompt-facing only:
  the schema and every runtime voice are untouched; the golden_020
  request pins carry the new description bytes.

## testing

Pane's suite, by name, in pane's order, against httptest servers and
injected seams (no live SearXNG, no live proxy, no required trafilatura);
the exec-driven cases now run through the one tool's dispatch with the
`{"action": ...}` shape:

fetch (pane's web-fetch.test.mjs order):

- ipIsPrivate: v4 table
- ipIsPrivate: v6 table
- ipIsPrivate refuses unnormalized loopback spellings (hex 4-in-6,
  zero-padded, unparseable)
- non-http(s) schemes are refused
- private hosts are refused before any connection
- redirects are followed and each hop re-guarded
- a redirect into private space is refused
- redirect loops stop at the hop cap
- an oversized Content-Length is refused before download
- the body stream is capped even when headers lie
- binary content types are refused
- non-2xx status is an error
- htmlToText strips script/style, keeps structure, decodes entities
- capChars truncates loudly with the true total
- extractReadable falls back to htmlToText when trafilatura is unavailable
- e2e: real server through the seam, html extracted
- e2e: timeout surfaces as a clear error
- execute reports guard refusals as tool errors, not throws
- tool registration: one name `web`, required action/target, guidelines exist

search (pane's web-search.test.mjs order):

- query is encoded and sent to the local SearXNG JSON API
- results map to title/url/snippet with tags stripped and snippet capped
- maxResults slices, default is 5
- missing fields degrade to empty strings, empty results say so
- SearXNG being down surfaces as a loud error
- schema requires action/target and bounds maxResults, maxChars, timeoutMs

rig-side named cases (the port's own surface):

- the dial is pinned to the vetted addresses and never re-resolves
  (a loopback listener behind a lying resolver is not reached)
- the egress proxy is used when set (the proxy sees the request)
- an unreachable proxy names itself and the fix
- the trafilatura fallback is announced in the content (rig over pane)
- trafilatura resolution: shared venv first, then PATH, explicit wins
- the search URL is built in pane's key order
- the search respects the caller ctx (the transport seam sees the
  request context; expiry surfaces as an error and does not hang)
- the fold's own cases: an unknown action refuses naming the two
  values, action and target are both required, out-of-range optionals
  refuse instead of panicking, and the other action's optionals are
  ignored

Skip gates: the trafilatura-present cases skip cleanly when neither the
shared venv nor PATH has the binary; everything else needs no network
beyond loopback httptest servers, so the suite is green on a bare box.

## scope

One leaf package (three files, one tool), one registration line at the
root, the allow-list default's `web_search,web_fetch` pair shrinking to
`web` (one entry, per-tool-name approval), three env knobs read in main
(RIG_SEARXNG_URL, RIG_WEB_FETCH_PROXY, RIG_TRAFILATURA) unchanged. The
loop is byte-identical; the wire goldens re-pin once with the fold.
