# tool/view: pixels the model can look at

A vision model reads an image only if the harness puts the bytes on the wire.
`view` is that tool. Since 3.4.0 it is an action door with three sources —
`image` (the default, and 3.3.5's one verb), `video`, `browser` — and all three
produce the same one thing: one line of text, `[[rig:image …]]`, with the
image itself carried beside that line by the provider that knows how. Named
`view` rather than `read` so the text tool stays the text tool and each keeps
a schema a local model reaches for.

## goals

- `view {path}` (image, the default): decode png, jpeg, webp, or the first
  frame of a gif; downscale so the longest side is at most 1568 px; re-encode;
  hand back one line naming what was stored.
- `view {action:"video", path}`: sample a clip with ffmpeg and reply one
  contact sheet, or `at` for one frame. Frames ride the image path; nothing
  downstream learns what a video is.
- `view {action:"browser", step:…}`: drive one headless page per session over
  the DevTools protocol on `--remote-debugging-pipe`, stdlib only, and reply
  a numbered list of the page's interactive elements plus a screenshot when
  the step is `open` or `shot`, or `shot:true` asks for one.
- The image travels as content-addressed bytes under the rig home, so the
  same input always produces the same bytes and the same wire prefix.
- The provider turns that line into the OpenAI/Anthropic image part, placed
  where its format allows it.
- `vision` on a model row decides whether the tool exists at all.

## non-goals

- No inline image rendering: the TUI prints a row, not pixels.
- No image writing, cropping, annotating, or multi-frame gif handling.
- No blob garbage collection; named, and left to a later sweep.
- No second provider wire: `provider/openai` and `provider/anthropic` are the
  only adapters that know about image parts, and neither learns a new source.
- No change to `core/`, `loop/`, `core.ToolExec`, or the sessions schema: the
  marker is ordinary tool-result text, so the transcript, the recorder,
  compaction, and resume all carry it untouched.
- Video is frames, not playback; audio is irrelevant. The browser is one
  page, refs, clicks and typing — no tabs, no JavaScript evaluation as a
  tool, no pixel coordinates, no network interception beyond `open`.
- `ffmpeg` and the browser are named by the operator (`RIG_*` or settings);
  `view` installs neither.

## the contract, stated once

Two facts are shared by three packages (the tool that writes, the provider
that reads, the frontend that prints), so they live in one stdlib-only leaf,
`imagemarker`, beside `pathguard`: the one format and the one blob path rule.
Each consumer re-implementing them would be a drift waiting to happen — the
bytes are the prompt cache, and the cache is byte-keyed.

    [[rig:image sha256=<hex> mime=<type> w=<w> h=<h> orig=<ow>x<oh> bytes=<n> src=<path>]]

one line, fixed key order, the tolerant value last. `imagemarker.Format`
writes it, `imagemarker.Parse` reads exactly one marker line, and
`imagemarker.BlobPath(dir, sum)` is `<dir>/<hex>` with no extension: the mime
is in the marker, and a second name for the same bytes is how a content
address becomes a path. `src` may carry spaces, `]]`, and key lookalikes, but
no control character — a newline there would split the reply, and the second
line could be a marker the tool never wrote, so `tool/view` refuses such a
path before the stat and `Parse` refuses such a `src` outright.

A browser step's reply is text and *ends* with the marker line: the numbered
element list first, the image last. `imagemarker.ParseLast` is the reader's
rule (3.4.0): the provider honors a `view` result whose last line is a marker,
and only a `view` result. Every line above it is `view`'s own, and the tool
sanitises page-derived text — control characters become spaces, runs
collapse, `[[rig:` is neutralised, names are cut at 80 runes, the list at 40
elements — so a page cannot smuggle a marker line into the reply. The
provider stays ignorant of videos and browsers: it reads the last line, and
the image is the image.

`orig` is the pre-scale dimensions: the decoded source for an image, the
composed sheet for a video, the captured viewport for a screenshot.

## layout

```
imagemarker/                     the shared contract, stdlib only
  marker.go                      Ref, Format, Parse, ParseLast, BlobPath,
                                 BlobsDir, HasControl, Find
  marker_test.go
tool/view/                       the tool
  view.go                        Config, the action door, the image path,
                                 publish (resample, encode, blob, marker)
  video.go                       ffmpeg probe/extract, caps, the sheet's frames
  sheet.go                       the contact-sheet grid and its labels
  browser.go                     the session's browser: queue, steps, lifecycle
  browserpage.go                 handshake, navigate, ready polls, snapshot,
                                 screenshot, the death description
  browserguard.go                the URL walls, the binary, the flags
  browserreply.go                the page line, the element list, the sanitiser
  cdp.go                         the DevTools pipe client (NUL-delimited JSON)
  page.js                        the element snapshot the page evaluates
  view_test.go, video_test.go, browser_test.go, browser_internal_test.go
provider/openai/                 image parts on the wire
  images.go, messages.go         the content shape, the honor rule, the blob read
provider/anthropic/              the same, as tool_result content
frontend/tui/                    the row
  commit.go                      view's detail: path, dimensions, size
models/ Model.Vision             config: the `vision` key
config/                          settings.ffmpeg, settings.browser
cmd/rig/                         registration, the concurrency set, the allow
                                 default, the close at session end
store/scheduler/jail.go          RIG_FFMPEG/RIG_BROWSER on the jail env and a
                                 read-only bind of each binary's directory
```

## decisions

- **Formats are what the bytes say.** `image.DecodeConfig` picks the decoder
  from the magic, never from the extension; anything outside png, jpeg, webp,
  and gif refuses, naming the file. Lossy and lossless webp both decode
  (the `x/image/webp` decoder covers VP8, VP8L and VP8X), and an alpha webp
  leaves as a PNG.
- **Two caps, both loud.** The source is refused over 20 MiB before it is
  read (an `io.LimitReader` past the cap, so a file that grows mid-read
  cannot exceed it either), and a source whose header claims more than 1<<24
  pixels is refused before anything is allocated: a 20 MiB PNG can decompress
  to hundreds of megabytes, and the header is the only place that fact is
  known cheaply. Both refusals name the cap and the way around it.
- **The bound is 1568 on the longest side**, the number the vision encoders
  of this family tile at. Below it nothing resamples; the re-encode still
  happens, because the blob is a function of the decoded pixels and not of
  the source file's encoder.
- **A box filter, one module, for the decoder alone.** The resampler is an
  area-average over the source rect each destination pixel covers: about
  forty lines, deterministic, and better than a bilinear for shrinking.
  `golang.org/x/image/draw` stays out even though `webp` comes in from the
  same module — the filter is not the reason for the dependency, and saying
  so is the point. `golang.org/x/image v0.45.0` is the one module added,
  pinned below v0.46 so `golang.org/x/sys` does not move with it; only
  `webp` and the label face (`font/basicfont`, for the sheet's timestamps)
  are imported, and the Go stdlib alone cannot read webp or draw text.
- **Re-encode rule**: `image/jpeg` at quality 90 for an opaque image whose
  source was a lossy still (`jpeg`, `webp`); `image/png` otherwise, which is
  every transparent source and every lossless one. Video frames and the
  contact sheet are lossy and opaque, so they leave as JPEG; a browser
  screenshot is lossless, so it leaves as PNG. Deterministic either way;
  `mime=` is the mime of the bytes sent, the blob's own bytes, not the
  source's.
- **Written once, atomically, never by path.** `<RIG_HOME>/blobs/<sha256>`:
  the digest over the encoded bytes, written to a nameless temporary in the
  same directory and renamed, so a reader never sees a partial blob and a
  replay never truncates a live one. An existing blob with that name is kept
  as it is — same address, same bytes, that is the whole point. The key is
  never the source path: a changed file gets a new address, and history
  cannot be re-pointed at new bytes.
- **The provider honors a marker only when the tool that wrote it was
  `view`**, and only the reply's last line. `tool_call_id` back to the
  assistant message that owns the current tool batch — the batch runs from
  that assistant message until the next message that is not a tool result —
  never through the transcript as a whole, so a call id reused on a later
  `read` is looked up in the turn that issued it and the marker stays text.
  A marker inside a `read` line, a `web` fetch body, or a user or assistant
  message is text. The rule is at the encoder, so nothing else can grant it.
- **Placement is the format's**: every tool message of one assistant turn is
  contiguous, so the synthetic user message goes after the *last* tool
  message of the batch, one per view, in tool-call order. Encoding is pure —
  transcript and blobs in, bytes out — so the cached prefix survives turn
  after turn and two assemblies of one transcript are byte-identical.
- **A bad blob never fails a request.** A missing, unreadable, oversized, or
  digest-mismatched blob degrades to the tool message plus a text note naming
  the sha256 and the reason: a corrupt cache costs the picture, not the turn.
  The digest is recomputed from what is read, so a blob whose bytes no longer
  match its address is treated as missing, not as an image.
- **The menu pays for nothing it does not use.** For a text row the tool
  table is byte-identical to 3.3.5 — the wire golden does not move; a vision
  row's menu carries `view`'s entry, which grew from 469 to 1,731 characters
  with the door, video and browser.
- **Gating is registration, not a runtime check.** `view` joins the native
  tool table only when the active row has vision, and the provider carries
  the same fact, so a non-vision model never sees the schema and never gets a
  base64 part it cannot read. The session can switch either way mid
  transcript: the switch rebuilds the provider and the tool table; a switch
  off closes the browser.
- **The row's flag, the operator's call.** `vision` defaults to false and the
  embedded table sets it for nobody: rig cannot know what a local server
  actually serves.
- **`src` is the absolute path** for an image or a video, after the `~`
  boundary, the same reading `read`'s replies use; for a browser step it is
  the page's own URL, sanitised. The TUI row prints it.
- **The door defaults to `image`.** `tool.Mutates("view","")` reads the
  registry's `defaultAction`, so every 3.3.5 call shape stays a read that
  runs beside its admitted neighbours; the registry's `mutates:["browser"]`
  marks the actions that can submit a form.

## video

- **The binary is a knob**: `RIG_FFMPEG` over `settings.ffmpeg`, and `ffmpeg`
  on `PATH` when neither names one. A missing or non-executable binary
  refuses naming both knobs; a jailed worker gets the same env and a
  read-only bind of the binary's directory (`store/scheduler/jail.go`),
  the `RIG_PYTHON` way.
- **The caps sit beside the image caps**: 512 MiB of source (a stat, before
  ffmpeg runs), 3600 s of clip for a sheet (ffmpeg's own duration line, read
  once), and 1..12 frames (default 6). Each refusal names the cap and the way
  around it: trim, or take one frame with `at`.
- **A single `at`** parses seconds or `[hh:]mm:ss`, seeks before the input
  (fast and accurate when re-encoding), pulls one frame as PNG on stdout,
  decodes it, and publishes it like any image: the 1568 bound, the re-encode
  rule, the content address. `at` is not bounded by the clip cap: one frame
  of a long recording is exactly the way around it.
- **The sheet** extracts `frames` frames at the middle of each equal slice of
  the duration, in one ffmpeg call per frame (`-ss <t> -i <path> -frames:v 1
  -f image2pipe -c:v png -`), decodes each, and tiles them: the grid picks
  the column count that lets the tallest tiles stand (scale capped at 1, so
  a small clip is never upscaled), ties broken by the grid closest to square.
  Each tile is labelled with its timestamp (`m:ss.t`) drawn with
  `basicfont.Face7x13` on a dark strip; the sheet's own size is the tiles
  plus a 4 px gutter, under 1568 on both sides. Frames are re-sampled by the
  one box filter, so the sheet is a function of the clip and the arguments —
  the same file and `at`/`frames` give the same blob.
- **A 20 s probe and a 20 s extraction, each ctx-bound**, so a hung ffmpeg
  cannot hold the turn; ffmpeg's stderr tail (8 KiB) rides every refusal.
- **No new dependency**: ffmpeg is an external binary the operator names, and
  the label face is already inside `golang.org/x/image`.

## browser

- **One process per session, started on first use, kept across calls**, so
  the page keeps its state and its refs from one tool call to the next. It is
  closed at session end (`toolView.Close`, wired in `cmd/rig`) and when the
  row stops having vision; a death between calls is announced once, on the
  next call, in the python kernel's words: `note: fresh browser; the previous
  browser exited (code N)` plus the stderr tail. A deliberate close is not a
  death and leaves no note.
- **The binary is a knob**: `RIG_BROWSER` over `settings.browser`; no PATH
  fallback, because a browser is a choice, not an assumption. A missing knob
  refuses naming both. A jailed worker gets the env and a read-only bind of
  the binary's directory.
- **The protocol is the pipe**: `--remote-debugging-pipe` puts commands on
  the child's fd 3 and replies on fd 4; the client writes NUL-terminated
  JSON, routes replies by id, ignores events, and fails every pending call
  when the pipe closes. Stdlib only — no websocket, no dependency.
  `Target.createTarget` → `Target.attachToTarget {flatten:true}` →
  `Page.enable`/`Runtime.enable`; the session id rides every page command.
- **The flags are the sandbox's**: `--no-sandbox` is passed deliberately. The
  session already runs `bash`, so the browser carries no privilege the
  session does not have, and under bwrap Chromium's nested sandbox cannot
  start at all; the jail, not Chromium's sandbox, is the worker's boundary.
  `--disable-background-networking` keeps an idle browser off the network,
  `--window-size=1280x800` is under the 1568 bound so a screenshot needs no
  resample, and `--proxy-server` carries `webFetchProxy` when it is set.
- **Steps**: `open url`, `click ref`, `type ref text`, `scroll amount`
  (default one viewport), `shot`, `close`. Every step replies with
  `page <url> — <title>` and the numbered interactive elements
  (`[2] button "Sign in"`); `open` and `shot` always attach the screenshot,
  and the other steps attach one only when `shot:true`, because images are
  the expensive part of context. The element list is rebuilt by one page
  script (visible `a`, `button`, `input`, `select`, `textarea`, `summary`,
  and ARIA roles), and the model acts by ref, never by pixel. A ref the page
  no longer holds refuses and asks for a listing step.
- **Actions are serial within a session**: one queue in front of the
  process, so two steps in one batch cannot interleave commands on the pipe.
  The registry marks them mutating, because a click can submit a form.
- **The walls are web's**: an `http(s)` `open` passes `tool/web`'s SSRF
  guard (`GuardURL`: name resolution, private/loopback/link-local refused)
  and every request then rides `webFetchProxy` when configured; `file://`
  must resolve, and must sit under the session's workspace or the rig home
  (pathguard); `about:blank` and nothing else of `about:`; every other
  scheme refuses. The guard vets the URL the model asked for; subresources,
  redirects and later clicks inside the page are the proxy's job.
- **The page is untrusted text**: the title and the accessible names are
  sanitised (no control characters, no `[[rig:` prefix, 80 runes), and the
  reply's marker is written last, so the only marker the provider can honor
  is the one `view` wrote.

## testing

Named cases, the real filesystem in `t.TempDir()`, the real encoders:

- `imagemarker`: format and parse round-trip; a src with spaces and with
  `]]` in it; a src carrying a control character is refused; a src carrying
  a whole marker line is refused; a line that is not a marker; two markers;
  a truncated one; the blob path rule; `ParseLast` takes the final line and
  refuses a reply that ends in text.
- `view`: the downscale bound (2560x1440 to 1568x882) and no resample inside
  it; the re-encode rule per source format, transparency included; the format
  refusal by magic, not extension; the 20 MiB cap; the pixel cap; a missing
  file; a path carrying a control character is refused before the stat, the
  smuggled-marker path included; an empty directory created on demand;
  identical input bytes giving a byte-identical blob and marker; a replay
  that keeps the blob it already wrote; the ctx bound.
- `view` video, from a generated `ffmpeg lavfi testsrc` clip in `t.TempDir()`:
  the default sheet is one marker line whose grid is the layout's own size,
  labels drawn, the same clip and arguments giving the same blob; large
  frames stay under 1568; one `at` is that frame alone at the bound, two
  timestamps differ, the same timestamp repeats; the frame-count, source-size
  and duration caps refuse naming themselves and the way around; an absent
  ffmpeg (and a configured-but-missing one) names the knob; bad stamps and a
  missing path refuse.
- `view` browser: the pipe client's id routing, NUL framing, error mapping
  and death (in-process, no browser); a local `httptest` page driven
  `open → type → click → shot`, asserting the refs, the form's POST landing
  on the test server with the value typed in an earlier call, the state
  carried across calls, the screenshot's marker and blob, `close`, and a
  fresh page after it; a killed browser named once on the next call; close
  leaving no note; the SSRF guard refusing loopback and link-local, the
  scheme refusal, `file://` passing pathguard, and the absent-binary
  refusal; an unknown step; eight concurrent steps serialized.
- The browser cases and the ffmpeg cases skip with the binary's name when it
  is absent; CI installs both as fixtures (the python venv's shape), so they
  run there.
- `provider/openai` and `provider/anthropic`: the image part lands after the
  last tool message of the batch, and nowhere else; a marker from another
  tool is not honored; a marker in a user or assistant message is not
  honored; a reused call id on a later `read` is not honored as `view`;
  a view result whose last line is text stays text; text above the marker
  keeps its image; two views in tool-call order; a missing blob giving the
  note and no fault; a non-vision provider sending the tool text alone; two
  assemblies of one transcript byte-identical; one over `httptest` with the
  whole loop proving the image part reaches the wire body.
- `models` and `config`: the flag defaults false; the file sets it; the merge
  overlays it; a non-boolean refuses by name; the unknown-key list names it;
  `ffmpeg` and `browser` parse, merge, and default empty.
- `cmd/rig`: `view` registered for a vision row and absent otherwise; a model
  switch taking effect on the next turn's table; the mutating set carrying
  browser actions but not the default image; the embedded allow growing by
  one; the wire golden unchanged for a non-vision row; the session closing
  the browser at exit.
- `store/scheduler`: `RIG_FFMPEG`/`RIG_BROWSER` on the jail env when set,
  and a read-only bind of a binary's directory outside the standard roots.
- `frontend/tui`: the row prints path, both dimensions, and the size, and
  prints no marker line and no image.

## scope

New: `tool/view/video.go`, `tool/view/sheet.go`, `tool/view/browser.go`,
`tool/view/cdp.go`, `tool/view/page.js`, `tool/web/guard.go` (the SSRF guard
as one exported door), this spec, and the package docs.
Changed: `tool/view/view.go` (the door, `publish`, the interface),
`imagemarker` (`ParseLast`), the two providers (the last-line rule),
`config` (`ffmpeg`, `browser`), `cmd/rig` (one construction, the close, the
two knobs through the worker wiring), `store/scheduler` (the jail env and
binds), `tool/delegate`/`swarm` (the same two knobs), the freeze allowlist's
new `tool/web/guard.go` (under the already-allowed `tool/web/`), and the docs.

`core/` and `loop/` are byte-identical; `core.ToolExec` keeps its
`(string, error)`; the sessions schema is untouched — the marker rides as
tool-result text.

**The frozen boundary.** No frozen path moves. `tool/view/` already sits in
the freeze allowlist and the new files are under it; `tool/web/guard.go` is
under `tool/web/`, `imagemarker/`, `provider/`, `config/`, `models/`,
`cmd/rig/`, `store/` and `docs/`/`specs/` are allowlisted. Nothing under
`core/`, `loop/`, `policy/`, or `middleware/` is touched.
