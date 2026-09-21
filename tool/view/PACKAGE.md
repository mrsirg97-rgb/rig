# tool/view

## What it is

`view`: look at an image. One path in, one line out. The bytes go to a
content-addressed store and the provider carries them to the model as a
real image part; the transcript keeps only the line, so a screenshot costs
its reference. The tool is registered only for a model row with `vision`
(SPEC_VIEW).

## What it includes

- `view.go`: the schema, the caps, the decode, the box resample, the
  re-encode, and the blob write. The reply is `imagemarker.Format`'s line
  and nothing else.

## How it is consumed

- Registered at the root beside the other read-only natives: concurrent,
  never mutating, in the embedded allow by default (an allow entry for a
  tool the row does not register is inert, so a text-row session pays
  nothing for it).
- `provider/openai` reads the blob the line names; `frontend/tui` prints
  the row from it. Neither trusts the other's copy of the format: both call
  `imagemarker`.

## Gotchas

- The caps are the whole safety story: 20 MiB of source, refused before the
  read; 1<<24 pixels of header, refused before the allocation (a 20 MiB PNG
  can decompress into hundreds of megabytes, and the header is the only
  place that fact is known cheaply). Both refusals name the cap and the way
  around it.
- The format is the bytes: `image.DecodeConfig` sniffs the magic, so a
  `.png` carrying a bitmap refuses and a `.dat` carrying a JPEG works.
  Outside png, jpeg, webp, and the first frame of a gif there is no reply.
- The blob is written once, through a temporary in the same directory and a
  rename, and an existing blob at that address is never rewritten — same
  address, same bytes, that is the promise. The source path is never part
  of the key, so a changed file gets a new address and an old transcript
  cannot be re-pointed at new pixels.
- A lossy source that stays opaque leaves as JPEG; anything with alpha
  leaves as PNG. The decision is per image, from the decoded pixels, so the
  same file always yields the same blob.
- The bound (1568 px on the longest side) is the vision encoder's tiling,
  not a preference. Inside it nothing resamples, but the re-encode still
  happens: the blob is a function of the pixels, not of the source file's
  encoder.
- `view` never records anything in the session's file state: what the model
  saw was a downscale, which is not a license to `edit` the file.
- The lossless webp decoder comes from `golang.org/x/image/webp`; the
  resampler is local and stdlib, and `x/image/draw` is deliberately not a
  dependency.
