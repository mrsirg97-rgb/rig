# stats

## What it is

The machine's vitals, read from the world (SPEC_ROOM's system stats,
built 3.3.0): the load, the uptime, the RAM, the CPU's busy share, the
chip temperatures, the cards, the real filesystems, and the links that
move bytes. Nothing here is a store's row and nothing is replayed — the
same rule the room reads residency by. The one consumer is the
dashboard's room page (`frontend/web`'s `/api/room`), which renders it as
one band above the model cards; the package knows nothing of HTTP.

## What it includes

- `Read(ctx) *Sample`: one walk of the sources. It never fails as a
  whole and never returns nil — a source that is missing, unreadable, or
  (for `nvidia-smi`) slow past `gpuTimeout` is simply absent from the
  sample, so a box with one GPU, no sensors driver, and no `/sys` still
  answers with what it has. On Linux the walk reads `/proc/uptime`,
  `/proc/loadavg`, `/proc/meminfo`, `/proc/stat`, `/proc/mounts`,
  `/sys/class/hwmon`, `sysfs` net counters, `statfs`, and
  `nvidia-smi`; a non-Linux build answers only the cards, since the rest
  of the paths are Linux's.
- `(s *Sample) Report(prev) Report`: the wire shape, and the only
  exported one. The absolutes ride any read; the rates — the CPU's
  percent and a link's bytes per second — are a difference between two
  samples, so `Report(nil)` carries a core count with no percent and
  byte totals with no rate. A first read after a page opens says
  nothing about speed rather than inventing a number from one point.
- `NewSampler(read)` and `(*Sampler).Report(ctx)`: the stateful door the
  dashboard holds. It keeps the previous sample behind its own mutex,
  runs the read *outside* that lock — the lock covers the swap and the
  fold, never the two-second `nvidia-smi` — and advances the held sample
  only forward in time, so a read that arrives older answers no rate and
  never becomes the window. A nil read is `Read` itself: a caller names a
  source only to replace it.
- The selection rules, all of them pure functions over parsed values:
  one temperature row per chip — its hottest *labelled* input, because a
  chip's several zones are one die's several corners and the hot one is
  the news, and where the board's embedded controller reads the same
  package the CPU reports itself (`asusec`/`asus` beside
  `k10temp`/`coretemp`) the CPU's own word survives and the board's
  yields, so the band's rows say different things; one disk row per
  device, at its shortest mount point, largest filesystem first, the
  filesystem allow-list refusing tmpfs (RAM), overlay (one row per
  container), squashfs (a read-only core) and the pseudo filesystems (the
  kernel's furniture); one link row per interface that is not the
  loopback, not container plumbing (the `veth`, `br-`, `docker`, `virbr`,
  `tap` prefixes), and has moved a byte since boot, busiest first.
- The parsers state their own arithmetic: `/proc/uptime`'s first field and
  `/proc/loadavg`'s first three fields are the seconds and the averages;
  the RAM row needs *both* `MemTotal` and `MemAvailable`, an available
  share read against a total the kernel did not state being a guess; the
  busy share is the aggregate `cpu` line's idle share crossing between
  samples over its first eight columns — the guest columns left out
  because the kernel counts guest time inside user — and is refused for a
  counter that fell, a window no jiffies crossed, or an idle share larger
  than the window. hwmon inputs are millidegrees Celsius, and a chip that
  labels none of its sensors renders no row.
- `parseNvidiaCSV`: one row per card from
  `nvidia-smi --query-gpu=<index,name,utilization.gpu,temperature.gpu,
  power.draw,memory.used,memory.total> --format=csv,noheader,nounits`. A
  field the card refuses — the `[N/A]` of a power draw it cannot sense —
  is absent, never zero; the driver's own error line is no rows at all.
  The number is nvidia-smi's own, which counts by PCI bus while CUDA
  counts fastest-first, so the wire key is `pci_index` and the band's row
  says `pci0` and never a bare index: an operator reading the wrong card
  is the bug.

## The caps and their derivations

- `bandRows` is 4: the band shares the phone's screen with one model
  card, whose served table shows 12 rows (`servedShown` in
  `frontend/web/room.go`), so a quarter of that per section keeps the
  machine under one scroll. It caps the temperature rows, the disk rows,
  and the link rows, keeping the hottest, largest, and busiest.
- `gpuRows` is 8: a dense PCIe box's width. A box with more cards still
  holds them and they still serve; they just do not make the band.
- `gpuTimeout` is 2 s: half the room page's 4 s poll
  (`ROOM_REFRESH_MS` in `static/app.js`), the only sampler there is. A
  source slower than that is late for the read that asked, and the band
  renders without cards for that poll rather than holding the whole
  room's reply.

## How it is consumed

- `frontend/web`'s `handleRoom` samples on every `/api/room` read through
  one `*stats.Sampler` the server is built with, so two open pages answer
  their own windows and never serialize behind each other's subprocess.
  The page's poll is the only clock: a closed page samples nothing, which
  is what SPEC_ROOM asks for. Nothing in the package starts a timer, a
  goroutine, or a cache.

## Gotchas

- The rate's denominator is the elapsed wall time between the two
  samples, taken at the reads; the counters themselves carry no
  timestamp. A read that arrives before the one held (a stale tab's
  retry) answers no byte rate, since a negative span is refused; the
  CPU's percent needs no span, only jiffies that crossed forward.
- A byte counter that fell answers no rate: a `netns` move, a driver
  reload, or an interface reset zeroes it, and the window is the
  machine's, not this program's.
- `statfs` is asked of the *mount point*, not the block device: a ZFS or
  LUKS root answers nothing at the device path and everything at `/`.
- The CPU percent is the aggregate `cpu` line's idle share crossing
  between samples, so a container that cannot read host `/proc/stat`
  answers the container's own jiffies. Named, not fixed.
