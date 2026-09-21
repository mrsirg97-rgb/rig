# tool/view: an image the model can look at

A vision model reads an image only if the harness puts the bytes on the wire.
`view` is that tool: a path in, one line of text out, and the image itself
carried beside that line by the provider that knows how. Named
`view` rather than `read` so the text tool stays the text tool and each
keeps a schema a local model reaches for.

## goals

- `view {path}`: decode png, jpeg, webp, or the first frame of a gif;
  downscale so the longest side is at most 1568 px; re-encode; hand back one
  line naming what was stored.
- The image travels as content-addressed bytes under the rig home, so the
  same input always produces the same bytes and the same wire prefix.
- The provider turns that line into the OpenAI-format image part, placed
  where the format allows it.
- `vision` on a model row decides whether the tool exists at all.

## non-goals

- No inline image rendering: the TUI prints a row, not pixels.
- No image writing, cropping, annotating, or multi-frame gif handling.
- No blob garbage collection; named, and left to a later sweep.
- No second provider wire: `provider/openai` is the only adapter that knows
  about image parts.
- No change to `core/`, `loop/`, `core.ToolExec`, or the sessions schema: the
  marker is ordinary tool-result text, so the transcript, the recorder,
  compaction, and resume all carry it untouched.

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
address becomes a path. The tool replies with the line and nothing else,
and the line stays one line: `src` may carry spaces, `]]`, and key
lookalikes, but no control character — a newline there would split the
reply, and the second line could be a marker the tool never wrote, so
`tool/view` refuses such a path before the stat and `Parse` refuses such a
`src` outright.

`orig` is the named addition to the line the deliverable asked for. The TUI
row is specified as `shot.png · 2560x1440 -> 1568x882 · 412 KB`, and the tool
result is the only channel from tool to frontend, so the pre-scale dimensions
have to travel in it. It sits before `bytes` and `src` so every pinned field
keeps its order and `src`, the one value that may contain a space, stays
right where the line ends. Additive: a reader that ignores it still reads the
line, and the bytes stay a function of the decoded pixels.

## layout

```
imagemarker/                     the shared contract, stdlib only
  marker.go                      Ref, Format, Parse, BlobPath, IsMarker
  marker_test.go
tool/view/                       the tool
  view.go                        the schema, the caps, the scale, the blob
  view_test.go
provider/openai/                 image parts on the wire
  images.go                      the content shape, the honor rule, the blob read
frontend/tui/                    the row
  commit.go                      view's detail: path, dimensions, size
models/ Model.Vision             config: the `vision` key
cmd/rig/                         registration, the concurrency set, the allow default
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
  `webp` is imported, and the Go stdlib alone cannot read webp.
- **Re-encode rule**: `image/jpeg` at quality 90 for an opaque image whose
  source was a lossy still (`jpeg`, `webp`); `image/png` otherwise, which is
  every transparent source and every lossless one. Deterministic either way;
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
  `view`.** `tool_call_id` back to the assistant message that owns the
  current tool batch — the batch runs from that assistant message until the
  next message that is not a tool result — never through the transcript as
  a whole, so a call id reused on a later `read` is looked up in the turn
  that issued it and the marker stays text. The result must be exactly the
  marker line and nothing else, which is what `view` replies; a result that
  carries a smuggled line is text too. A marker inside a `read`, a `grep`
  line, or a `web_fetch` body is text, and a marker in a user or assistant
  message is text too. The rule is at the encoder, so nothing else can grant
  it.
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
  table is byte-identical to 1.2.16 — the wire golden does not move;
  a vision row's menu carries `view`'s 457 extra characters over the same
  shape, well inside the menu bound SPEC_CORE pins.
- **Gating is registration, not a runtime check.** `view` joins the native
  tool table only when the active row has vision, and the provider carries
  the same fact, so a non-vision model never sees the schema and never gets a
  base64 part it cannot read. The session can switch either way mid
  transcript: the switch rebuilds the provider and the tool table, images
  already in the transcript simply stop being attached, and the marker line
  keeps the history readable.
- **The row's flag, the operator's call.** `vision` defaults to false and the
  embedded table sets it for nobody: rig cannot know what a local server
  actually serves.
- **`src` is the absolute path**, after the `~` boundary, the same reading
  `read`'s replies use; the TUI row prints it.
- **No drift license.** `view` does not touch `Session.Files`: what the model
  saw was a downscale, and that is not a license to `edit` the file.

## testing

Named cases, the real filesystem in `t.TempDir()`, the real encoder:

- `imagemarker`: format and parse round-trip; a src with spaces and with
  `]]` in it; a src carrying a control character is refused; a src carrying
  a whole marker line is refused; a line that is not a marker; two markers;
  a truncated one; the blob path rule.
- `view`: the downscale bound (2560x1440 to 1568x882) and no resample inside
  it; the re-encode rule per source format, transparency included; the format
  refusal by magic, not extension; the 20 MiB cap; the pixel cap; a missing
  file; a path carrying a control character is refused before the stat, the
  smuggled-marker path included; an empty directory created on demand;
  identical input bytes giving a byte-identical blob and marker; a replay
  that keeps the blob it already wrote; the ctx bound.
- `provider/openai`: the image part lands after the last tool message of the
  batch, and nowhere else; a marker from another tool is not honored; a
  marker in a user or assistant message is not honored; a reused call id on
  a later `read` is not honored as `view` (exactly one image on the wire);
  a view result carrying a smuggled marker line is text; two views in
  tool-call order; a missing blob giving the note and no fault; a non-vision
  provider sending the tool text alone; two assemblies of one transcript
  byte-identical; one over `httptest` with the whole loop proving the image
  part reaches the wire body.
- `models` and `config`: the flag defaults false; the file sets it; the merge
  overlays it; a non-boolean refuses by name; the unknown-key list names it.
- `cmd/rig`: `view` registered for a vision row and absent otherwise; a model
  switch taking effect on the next turn's table; the concurrency set carrying
  it, the mutating set not; the embedded allow growing by one; the wire golden
  unchanged for a non-vision row.
- `frontend/tui`: the row prints path, both dimensions, and the size, and
  prints no marker line and no image.

## scope

New: `imagemarker/` (one file plus its tests), `tool/view/` (one file plus its
tests), `provider/openai/images.go`, this spec, and the package docs.
Changed: `models` (`Model.Vision`), `config` (the `vision` key, its row
table), `cmd/rig` (one import, one name in `nativeToolNames`, one in
`concurrentNatives`, the gated registration, the provider options, the
embedded `allow`), `provider/openai` (`wireMessage.Content`'s type,
`wireMessages`' signature), `frontend/tui` (one detail function and one
preview skip), the freeze allowlist, and the docs.

`core/` and `loop/` are byte-identical; `core.ToolExec` keeps its
`(string, error)`; the sessions schema is untouched — the marker rides as
tool-result text.

**The frozen boundary.** No frozen path moves. Two pure paths join the
freeze allowlist: `imagemarker/` (a leaf with no imports of rig at all, the
neighbour `pathguard/` already occupies) and `tool/view/` (a tool, which is
what `tool/` holds). Nothing under `core/`, `loop/`, `policy/`,
`middleware/`, `store/`, `frontend/tui/` beyond the one detail function, or
`cmd/rig/`'s loop is touched by this change; the tool table, the model row,
the provider constructor and the ctx are the seams it rides, each already
there for that job.
