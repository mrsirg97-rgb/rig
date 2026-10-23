# tool/view

## What it is

`view`: look at pixels. Since 3.4.0 it is an action door with three sources
— `image` (the default; 3.3.5's one verb), `video`, `browser` — and one
reply shape: one `[[rig:image …]]` line, with the bytes in the
content-addressed store and the provider carrying them to the model as a
real image part. The transcript keeps only the line, so a screenshot costs
its reference. The tool is registered only for a model row with `vision`
(SPEC_VIEW).

## What it includes

- `View`: the tool as its interface: `tool.Definition` (the words), `Exec`
  as the one JSON door — decode and route, nothing else — and the three
  verbs `Image(ctx, path)`, `Video(ctx, Clip)`, `Browser(ctx, Step)`, plus
  `Close`. The path-required refusal, the blob-store gate, the caps and the
  decode live in the verb, so a Go caller and the model meet the same words
  and the same refusal; a cancelled ctx refuses before the filesystem is
  touched. `New(blobsDir)` is the image-only shape; `NewWith(Config)`
  carries the knobs (`FFmpeg`, `Browser`, `Proxy`, `Cwd`, `RigHome`, and
  the lookup and dial seams for the SSRF guard and the egress). The struct
  is unexported.
- `view.go`: the action door, the image path, and `publish` — resample to
  the 1568 bound, re-encode by the lossy/opaque rule, write the blob once,
  format the marker. Video frames and screenshots hand `publish` pixels;
  they do not copy the image path.
- `video.go`: ffmpeg's probe and frame extraction, the three caps (source
  bytes, clip seconds, frame count), the timestamp parser. One frame per
  `-ss`, PNG on stdout, decoded in Go so the blob never depends on ffmpeg's
  encoder bytes.
- `sheet.go`: the contact sheet — the grid that lets the tallest tiles
  stand, the 4 px gutter, the timestamp labels (`basicfont.Face7x13`), the
  dark backing. Deterministic: the same clip and arguments give the same
  blob.
- `browser.go`: one headless page per session, started lazily and kept; the
  serial queue; the steps open/click/type/scroll/shot/close; the death note.
- `browserpage.go`: the page's side of the pipe — target handshake, navigate,
  the ready polls, the element snapshot, the screenshot, the exit words.
- `browserguard.go`: the one guard (web's SSRF guard for `http(s)`,
  pathguard for `file://`, `data:`/`blob:` allowed), the binary resolution,
  and the flags. The CDP `Fetch` domain pauses every request, not only
  `open`'s, and hands each one to this guard.
- `browseregress.go`: the browser's only way out. A forward proxy on
  loopback, one per browser process, that Chromium is pointed at with no
  bypass (`--proxy-bypass-list=<-loopback>`, WebRTC held to proxied UDP).
  Every CONNECT and proxied request resolves its host through
  `web.PinHost`, refuses a private answer, and dials only the pinned
  address, so the address dialed is the address checked: a DNS rebind
  between the guard and the dial, and a WebSocket the `Fetch` domain never
  pauses, both meet it. A configured `webFetchProxy` is its upstream and is
  handed the pin, never the name.
- `browserreply.go`: the reply's lines — the page line, the capped element
  list, and the sanitiser that strips control characters and `[[rig:`.
- `cdp.go`: the DevTools client on `--remote-debugging-pipe` — NUL-delimited
  JSON on fds 3/4, replies routed by id, stdlib only.
- `page.js`: the one page script: visible interactive elements with role and
  accessible name, and the refs the steps click and type by.

## How it is consumed

- Registered at the root beside the other read-only natives: concurrent,
  never mutating by default (`defaultAction` is `image`), in the embedded
  allow by default. The registry's `mutates:["browser"]` marks the actions
  that can submit a form; the browser's own queue serializes them.
- `provider/openai` and `provider/anthropic` read the blob the line names;
  `frontend/tui` prints the row from it. Neither trusts the other's copy of
  the format: both call `imagemarker`, and both take the marker from the
  reply's *last* line.
- `cmd/rig` constructs it with the resolved `RIG_FFMPEG`/`RIG_BROWSER` and
  calls `Close` at session end and when the row loses vision.

## Gotchas

- The image caps are the whole safety story: 20 MiB of source, refused
  before the read; 1<<24 pixels of header, refused before the allocation.
  Both refusals name the cap and the way around it. Video adds 512 MiB of
  source, 3600 s of clip, and 1..12 frames, each with the same manner.
- The path may not carry a control character. The reply is a marker line (or
  ends in one), and a filename with a newline would split it. The refusal
  comes before the stat, and the marker's own parser refuses such a `src`
  too, so neither writer nor reader can be smuggled.
- The format is the bytes: `image.DecodeConfig` sniffs the magic, so a
  `.png` carrying a bitmap refuses and a `.dat` carrying a JPEG works.
  Outside png, jpeg, webp, and the first frame of a gif there is no reply.
- The blob is written once, through a temporary in the same directory and a
  rename; an existing blob at that address is never rewritten. The source
  path is never part of the key, so a changed file gets a new address and an
  old transcript cannot be re-pointed at new pixels.
- A lossy source that stays opaque leaves as JPEG; anything with alpha
  leaves as PNG. The decision is per image, from the decoded pixels, so the
  same file always yields the same blob. The re-encode quality is 90: high
  enough that screenshot text survives legibly, low enough that the blob
  stays a fraction of the source's bytes.
- The resampler polls the context every 32 output rows; the contact sheet and
  the screenshot ride the same filter and the same 1568 bound.
- ffmpeg is named by `RIG_FFMPEG`/`settings.ffmpeg`, falling back to PATH;
  the browser only by `RIG_BROWSER`/`settings.browser`. An unconfigured
  binary refuses naming its knob. A jailed worker gets both on its env and a
  read-only bind of each binary's directory.
- The browser is started with `--no-sandbox` on purpose: the session already
  has `bash`, and under bwrap Chromium's nested sandbox cannot start; the
  jail is the worker's boundary. `--disable-background-networking` keeps an
  idle browser off the network.
- The page is untrusted: names and titles are stripped of control characters
  and `[[rig:` before they enter the reply, and the marker is written last,
  so the only marker the provider honors is `view`'s own. The reader refuses
  a reply whose text above the marker carries the marker prefix, so a
  newline in a `src` cannot smuggle a second one.
- Every request is vetted at the paused stage: a redirect hop, a link, an
  image or a `fetch()` to a private address is failed with
  `BlockedByClient`, named in the step's reply, and the refused URL is capped
  like the rest of the text.
- The browser carries a parent-death signal (the same `execwrap` helper as
  the python kernel) so a SIGKILLed rig cannot orphan it or its profile.
- `view` never records anything in the session's file state: what the model
  saw was a downscale, which is not a license to `edit` the file.
- The lossless webp decoder and the label face come from
  `golang.org/x/image`; the resampler, the CDP client and the sheet are
  local and stdlib.
