# tool/view

## What it is

`view`: look at pixels. Since 3.4.0 it is an action door with two sources
— `image` (the default; 3.3.5's one verb) and `video` — and one reply
shape: one `[[rig:image …]]` line, with the bytes in the
content-addressed store and the provider carrying them to the model as a
real image part. The transcript keeps only the line, so a picture costs
its reference. The tool is registered only for a model row with `vision`
(SPEC_VIEW).

## What it includes

- `View`: the tool as its interface: `tool.Definition` (the words), `Exec`
  as the one JSON door — decode and route, nothing else — and the two
  verbs `Image(ctx, path)` and `Video(ctx, Clip)`. The path-required
  refusal, the blob-store gate, the caps and the decode live in the verb,
  so a Go caller and the model meet the same words and the same refusal; a
  cancelled ctx refuses before the filesystem is touched. `New(blobsDir)`
  is the image-only shape; `NewWith(Config)` carries `FFmpeg`. The struct
  is unexported.
- `view.go`: the action door, the image path, and `publish` — resample to
  the 1568 bound, re-encode by the lossy/opaque rule, write the blob once,
  format the marker. Video frames hand `publish` pixels; they do not copy
  the image path.
- `video.go`: ffmpeg's probe and frame extraction, the three caps (source
  bytes, clip seconds, frame count), the timestamp parser. One frame per
  `-ss`, PNG on stdout, decoded in Go so the blob never depends on ffmpeg's
  encoder bytes.
- `sheet.go`: the contact sheet — the grid that lets the tallest tiles
  stand, the 4 px gutter, the timestamp labels (`basicfont.Face7x13`), the
  dark backing. Deterministic: the same clip and arguments give the same
  blob.

## How it is consumed

- Registered at the root beside the other read-only natives: concurrent,
  never mutating (`defaultAction` is `image` and no action mutates), in the
  embedded allow by default.
- `provider/openai` and `provider/anthropic` read the blob the line names;
  `frontend/tui` prints the row from it. Neither trusts the other's copy of
  the format: both call `imagemarker`.
- `cmd/rig` constructs it with the resolved `RIG_FFMPEG`.

## Gotchas

- The image caps are the whole safety story: 20 MiB of source, refused
  before the read; 1<<24 pixels of header, refused before the allocation.
  Both refusals name the cap and the way around it. Video adds 512 MiB of
  source, 3600 s of clip, and 1..12 frames, each with the same manner.
- The path may not carry a control character. The reply is a marker line,
  and a filename with a newline would split it. The refusal
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
- The resampler polls the context every 32 output rows; the contact sheet
  rides the same filter and the same 1568 bound.
- ffmpeg is named by `RIG_FFMPEG`/`settings.ffmpeg`, falling back to PATH. An
  unconfigured binary refuses naming the knobs. A jailed worker gets it on
  its env and a read-only bind of its directory.
- `view` never records anything in the session's file state: what the model
  saw was a downscale, which is not a license to `edit` the file.
- The lossless webp decoder and the label face come from
  `golang.org/x/image`; the resampler and the sheet are local and stdlib.
