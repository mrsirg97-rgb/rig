# tool/rem

## What it is

Adapts `store/rem` to the loop's tool surface: runtime shape checks loud
at execute; session attribution from the threaded ctx (`memories.source`
defaults to the calling session id, `anon` when unthreaded, and accepts
free text when the caller passes one); replies exactly as the store shapes
them.

## What it includes

- `Tool` — a `core.Tool` over the rem store's read/write operations.

## How it is consumed

- Registered at the root as a native tool; the store's `policy/compact`
  `AutoReflect` seam feeds the reflection entry.

## Gotchas

- Session attribution comes from the threaded `core.SessionFrom`; an
  unthreaded call attributes `anon`.
- Replies are the store's shapes, verbatim — the adapter does not
  re-voice.
