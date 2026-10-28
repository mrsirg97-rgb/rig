# webtools/ — the machine-local web-tools bundle

See SPEC_WEBTOOLS.md. The leaf that owns `~/.rig/external`: the bundle's bytes
are embedded in the binary, so the bundle moves only with a release, and every
verb answers from what is measured on this machine, not from a remembered state.
It depends on `tool/web` for one thing — `ProxyDisplay`, so the dashboard and
fetch's own error say the same words about the same URL — and on nothing else.

- **One seam for the outside, one for the settings.** `Runner.Run(ctx, name,
  argv...)` carries docker's own voice back in the error, so a port conflict, a
  silent daemon and a missing binary are reported as *docker* said them;
  `exec.ErrNotFound` is the fact that means "not on PATH". `Setting` is
  `config.ReadKey`/`SetKey` behind `WithSetting`; the leaf names the key, never
  the path, and the door refuses a key `config` does not know.
- **The verbs run the bundle.** `start` creates the named volumes, runs the
  `chown` helper over each bind-mounted path (`-R 999:1000` for valkey,
  `-R 977:977` for the searxng cache — the images' own users, or first boot
  fails on a permission the operator cannot see), and only then
  `compose up -d --build`. `stop` runs `compose stop` and nothing else: the
  volumes keep their data, and no verb removes them.
- **Password and secret are reused, never reset.** `setup` keeps the password
  in the existing conf and `secret_key` in the existing `settings.yml`, so a
  re-run does not invalidate the limiter's bans or the static hashes. An
  explicit password is checked against `[A-Za-z0-9._-]{16,128}` *first for
  characters* — a space, quote, hash, `@`, `:` or comma is refused even in a
  long string, because the `BasicAuth` line is unquoted and the same bytes go
  into `settings.json`, where a `"` would break the JSON. Absent on both sides:
  32 generated hex for the password, 40 for the secret (the `secret_key` is a
  hex key SearXNG expects; 40 is its own convention, not a floor I invented).
- **Absence is a fact, not a fault.** `Status` never errors because docker is
  missing, the daemon is silent, a file is absent or `settings.json` cannot be
  read — those are fields (the last in `Stack`). Only a missing home, a missing
  `Runner` or a bundle file it cannot read comes back as an error, and
  `Status.Lines` prints every fact so the CLI and the page carry the same words.
- **On Linux the bundle carries its own egress firewall.** `WithGOOS` (default
  `runtime.GOOS`) picks the file set: `linux/` adds `docker-compose.firewall.yml`
  and `firewall/`, a host-network container with `NET_ADMIN` that writes the
  drop rules into `DOCKER-USER`, and whose healthcheck tinyproxy and searxng
  wait on; every compose call carries the override. Fetch pins the address only
  when it dials direct: through the proxy, tinyproxy resolves the name again and
  the deny-list matches strings, so only a rule on the connection's address
  closes DNS rebinding.
- **`setup` refuses before it touches anything.** A file that differs from what
  rig generates names itself and waits for `force`; a `webFetchProxy` that
  points at another host names itself and waits for `force`; credentials onto
  the same host are added without asking. Every refusal carries the `webtools:`
  prefix and says what to do about it.
- **Bounds, no clocks.** `capTail` keeps the last `maxRunBytes` (64 KiB) of a
  command's output and says how many bytes it dropped from the head; the
  `Runner` honors the caller's context, so the root's shutdown and command
  budget reach docker without this leaf keeping a timer of its own.
- **No healthcheck on the proxy** — a probe inside the container would hit
  tinyproxy's stock loopback `Deny` and report a lie; `status` answers from
  `docker compose ps --services --filter status=running`, which is measured.
