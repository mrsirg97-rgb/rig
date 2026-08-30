# tool/web: web_search and web_fetch

One leaf package, two tools: pane's web_search and web_fetch, ported.
Search talks to the local SearXNG instance (the ~/docker/web-tools
compose, :8888); fetch is a guarded HTTP reader (DNS re-check, redirect
re-guard, byte/char caps, egress proxy through the compose's tinyproxy
:8889) with HTML extraction via trafilatura and a stdlib fallback.
Stdlib only: net/http, net, os/exec; no third-party Go client.

## goals

- web_search: SearXNG JSON over net/http: endpoint from env
  (RIG_SEARXNG_URL, default pane's http://127.0.0.1:8888); results
  mapped to title/url/snippet with the 300-char snippet cap and the
  maxResults slice (1..20, default 5), loud `no results for "<query>"`.
- web_fetch: pane's guarded fetch verbatim: http(s) only, DNS resolution
  refuses private and link-local space with a readable error, every
  redirect hop re-checked, hop cap, textual content types only, declared
  Content-Length and streaming byte cap (5 MiB) with loud truncation,
  20 000-char cap with the named elision marker, 30 s default timeout,
  optional egress proxy from env (default http://127.0.0.1:8889, the
  web-tools compose's tinyproxy) with the unreachable-proxy fix-it voice.
- Extraction: trafilatura as a documented external (pane's own mechanism),
  resolved from the shared agent venv then PATH, overridable by
  RIG_TRAFILATURA; absent or failing degrades to pane's stdlib text
  pass and says so in the content.
- Pane's surface verbatim: descriptions, promptGuidelines, schemas, and
  every runtime voice.

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
  fetch tool carries the guard.

## layout

```
tool/web/
  web.go      package doc; the defaults (SearXNG, proxy) and the
              trafilatura resolution (shared venv -> PATH)
  search.go   web_search: the SearXNG call, the result mapping, the schema
  fetch.go    web_fetch: the guarded fetch, extraction, caps, the schema
  web_test.go pane's named cases in pane's order + the rig-side cases
```

`core/`, `loop/`, `middleware/`, `policy/`, `provider/`: untouched.

## interfaces

`core.Tool` as-is, two implementations in one package (the tool/file
pattern):

```go
// search.go; concrete types unexported with named constructors,
// the core/tool.go house shape
const DefaultSearXNG = "http://127.0.0.1:8888" // pane's PI_SEARXNG_URL default
type search struct{ /* searchURL, transport, the 15s budget */ }
func Search() *search                     // pane's default: the web-tools compose
func NewSearch(cfg SearchConfig) *search  // cfg: BaseURL (pane appends /search, so does this), Do seam
func (s *search) Name() string            // "web_search"

// fetch.go
const (
    DefaultProxy     = "http://127.0.0.1:8889" // pane's PI_WEB_FETCH_PROXY default
    maxBytesDefault  = 5 * 1024 * 1024         // pane's MAX_BYTES
    maxChars         = 20_000                  // pane's MAX_CHARS
    defaultTimeoutMs = 30_000                  // pane's DEFAULT_TIMEOUT_MS
    maxHops          = 5                       // pane's MAX_HOPS
)
type FetchConfig struct {
    Proxy       string        // egress proxy; "" = direct
    Trafilatura *string       // pane's string|null|undefined: nil = default resolution, &"" = off, &s = that binary
    Lookup      func(string) ([]string, error) // DNS seam; nil = system resolver
    Do          func(*http.Request) (*http.Response, error) // transport seam
    MaxBytes    int           // 0 = the default
}
type Fetched struct{ FinalURL string; Status int; ContentType string; Body string; BodyTruncated bool } // pane's Fetched
type fetch struct{ /* config, resolved trafilatura */ }
func NewFetch(cfg FetchConfig) *fetch  // the injection seam (pane's Deps)
func Fetch() *fetch                    // pane's defaults: proxy on, trafilatura resolved
func (f *fetch) Guarded(ctx context.Context, raw string) (Fetched, error) // pane's fetchGuarded

// shared surface, pane's functions verbatim
func IPisPrivate(ip string) bool            // pane's ipIsPrivate, same tables
func HtmlToText(html string) string         // pane's htmlToText (the RE2 port)
func CapChars(text string, max int) string  // pane's capChars
func ExtractReadable(html string, trafilatura *string) (string, string) // + the rig announcement footer
func DefaultTrafilatura() string            // shared venv -> PATH, "" when absent
```

Both `Description()`s fold pane's promptGuidelines after the description
(the python/scheduler house fold). `Schema()`s are pane's parameters,
hand-written: `required: ["query"]` / `required: ["url"]`, the integer
bounds (maxResults 1..20; maxChars min 100; timeoutMs min 1000).

## decisions

- **One leaf package, two tools.** pane ships the pair as two
  extensions; rig's design test wants one leaf package per
  capability family plus registration lines at the root. tool/file's
  three tools set the precedent.
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
- **The guard is check-and-use, per hop.** resolve, check all
  addresses, dial, and the Location of every redirect hop re-runs the
  same guardedUrl against the previous URL as base, before the next
  fetch. A redirect into private space is refused with the same voice as
  the first hop (pane's named case). The proxy is not guarded: it is
  loopback by construction, and guarding it would need a second policy.
- **Timeout is the whole fetch, all hops included**: pane's signal is
  created once outside the loop; Go's ctx carries the same shape
  (WithTimeout over the fetchGuarded call). A cancelled caller ctx
  returns the context error; the timeout voice names the ms and the
  current URL.
- **Byte cap is declared-then-streamed.** a Content-Length above the cap
  refuses before any download (pane's named case); otherwise the stream
  is read to the cap and the truncation is named in the content
  (`[TRUNCATED: download hit the byte cap; content is partial.]`), with
  the char cap applied after extraction and its own louder marker.
- **Voices are pane's verbatim**, including the fix-it: an unreachable
  proxy reads `egress proxy <url> is unreachable. Start it: cd
  ~/docker/web-tools && docker compose up -d`. The search error is
  `SearXNG search failed: HTTP <status>`; the fetch errors keep the
  `web_fetch: ` prefix pane's execute wraps. One named port difference:
  a refused connection is Go's voice (`dial tcp ...: connect:
  connection refused`), not Node's ECONNREFUSED; the named case asserts
  the loud shape, not the OS string.
- **Query strings are built by hand**, in pane's order
  (`?q=<escaped>&format=json`), not url.Values (which would sort the
  keys); the named case asserts pane's exact URL.

## testing

Pane's suite, by name, in pane's order, against httptest servers and
injected seams (no live SearXNG, no live proxy, no required trafilatura):

fetch (pane's web-fetch.test.mjs order):

- ipIsPrivate: v4 table
- ipIsPrivate: v6 table
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
- tool registration: name, required url, guidelines exist

search (pane's web-search.test.mjs order):

- query is encoded and sent to the local SearXNG JSON API
- results map to title/url/snippet with tags stripped and snippet capped
- maxResults slices, default is 5
- missing fields degrade to empty strings, empty results say so
- SearXNG being down surfaces as a loud error
- schema requires query and bounds maxResults

rig-side named cases (the port's own surface):

- the egress proxy is used when set (the proxy sees the request)
- an unreachable proxy names itself and the fix
- the trafilatura fallback is announced in the content (rig over pane)
- trafilatura resolution: shared venv first, then PATH, explicit wins
- the search URL is built in pane's key order
- the search respects the caller ctx (the transport seam sees the
  request context; expiry surfaces as an error and does not hang)

Skip gates: the trafilatura-present cases skip cleanly when neither the
shared venv nor PATH has the binary; everything else needs no network
beyond loopback httptest servers, so the suite is green on a bare box.

## scope

One leaf package (three files, two tools), two registration lines at the
root, the allow-list default growing by `web_search,web_fetch`, three env
knobs read in main (RIG_SEARXNG_URL, RIG_WEB_FETCH_PROXY,
RIG_TRAFILATURA). The loop is byte-identical.
