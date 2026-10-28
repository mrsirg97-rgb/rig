# webtools: the egress bundle, generated and repeatable

`rig webtools setup|start|stop|status` builds the bundle the `web`
tool talks to: SearXNG, its valkey, and the tinyproxy that carries
fetch's egress, all under one bridge network, in the rig home's
`external/` directory. The bytes come from templates embedded in the
binary; the machine's own values (the proxy password, the SearXNG
secret key) are generated once and reused thereafter; `webFetchProxy`
lands in `settings.json` with the credentials. This is the shape the
operator's hand-built bundle reached on 2026-10-26, measured: the
hardening is the template, so a new machine starts where the operator
finished instead of rediscovering it.

`specs/SPEC_WEB.md` owns the tool that consumes the bundle; this spec
owns the bundle and the verbs that build and run it.

## goals

- **Reproducible.** One command, three services, one network, no hand
  written files. Every machine gets the same bytes except the
  password and the secret key.
- **Hardened by default.** The measured set, not a starting point:
  `read_only` plus `no-new-privileges` plus memory and CPU caps plus
  log rotation plus non-root users, BasicAuth on the proxy, a
  destination deny-list, and the SearXNG limiter pinned to the
  network's gateway so host traffic is counted and relayed traffic
  is not. On Linux, an egress firewall drops the subnet's traffic to
  private address space by the address it goes to.
- **Honest about the world.** `docker` absent refuses and names the
  install path; a present `docker` whose daemon does not answer
  refuses with the daemon's own voice; a claimed port or subnet
  surfaces as docker's error, verbatim; existing files refuse and
  name themselves until `force` says otherwise.
- **Repeatable.** `status` reports what is there against what the
  generator would write, file by file; `setup` is byte-identical when
  re-run; `start` after a machine restart works with no re-generation.
- **Three surfaces, one behavior.** `rig webtools <verb>` (the shape
  `tool/web`'s fix-it names), `/webtools` in the frontends' command
  set, and the dashboard's update page. Each is a thin wire over the
  same `Tools`.
- **Settings, not folklore.** The proxy URL with credentials is
  written by `config.SetKey`, which refuses a key `config` does not
  know, and writes `0600` atomically.

## non-goals

- No teardown verb this iteration. `remove` deletes containers,
  volumes and the network; the volumes carry the limiter's bans and
  the valkey dump, so a destructive verb gets its own named cases and
  its own review later. `stop` is enough to leave.
- No `install.sh` change. The installer installs the binary; `rig
  webtools setup` is the first-run step, and an installer hook is a
  later iteration with its own measured path.
- No compose v1, no systemd, no boot-time start, no docker
  auto-install, no offline mode (the images pull from their registries
  as pinned).
- No tuning UI for the engines, the limiter or the deny-list. The
  bundle ships the measured defaults; the files stay editable and
  `status` names a hand edit as `differ` rather than pretending it
  never happened.
- No second bundle, no custom ports yet: 8888 and 8889 are `tool/web`'
  defaults, and the fixed bridge subnet is what `pass_ip` and the
  proxy's `Allow` line are written against.
- No healthcheck on the proxy: its image carries busybox `wget` but a
  loopback probe inside the container gets tinyproxy's stock loopback
  `Deny` (measured), so a healthcheck there would report a lie; the
  two services that can answer honestly carry healthchecks.

## layout

```
webtools/
  webtools.go     the Tools interface, New(home, opts...), the four verbs
  render.go       the template slots: the password, the secret, the ports
  docker.go       the Runner seam: real docker argv, capped output
  files/          the embedded bundle (compose, tinyproxy conf/filter/
                  Dockerfile, searxng settings.yml + limiter.toml)
  linux/          added on Linux: docker-compose.firewall.yml and
                  firewall/ (Dockerfile, egress.sh)
  webtools_test.go  named cases over a fake Runner
  PACKAGE.md      the package's spec file, citing this spec
command/webtools.go   the slash command over its own Env seams
frontend/web/webtools.go  the dashboard handler over the injected Tools
config/key.go         SetKey, the one settings writer (SetTheme moves
                      onto it)
cmd/rig/webtools.go   the `rig webtools <verb>` path, wired once
```

`core/`, `loop/`, `middleware/`, `policy/`, `provider/`: untouched.
`tool/registry.json`: untouched, so the model's words and the wire
render no diff.

## interfaces

```go
// webtools.go
type Tools interface {
	Status(ctx context.Context) (Status, error)
	Generate(ctx context.Context, password string, force bool) (string, error)
	Start(ctx context.Context) (string, error)
	Stop(ctx context.Context) (string, error)
}
type Option func(*config) // WithRunner(Runner), WithSetting(Setting)
func New(home string, opts ...Option) Tools  // dir = home/external

// docker.go — the one seam the tests fake; argv, never a shell
type Runner interface {
	Run(ctx context.Context, name string, argv ...string) (string, error)
}
func NewRunner() Runner // exec.CommandContext, output capped at maxRunBytes

// the settings door, wired to config.SetKey at the root
type Setting interface{ Put(key, value string) error }

// Status is read-only facts; its fields marshal as they read (the
// stats package's shape), and absence is a field, not an error
type Status struct {
	Dir      string   `json:"dir"`      // the bundle dir
	Docker   string   `json:"docker"`   // "ok", or the reason it is not
	Missing  []string `json:"missing"`  // bundle files absent, sorted
	Differ   []string `json:"differ"`   // files present that do not match, sorted
	Proxy    string   `json:"proxy"`    // settings.json webFetchProxy, redacted; "" when unset
	Services []string `json:"services"` // services compose reports running, sorted
}
```

`command/env.go` carries the same four verbs as func fields (the
`RemList`/`RemShow` shape) so the command leaf keeps its fakes;
`frontend/web` takes `WithTools(tools webtools.Tools)` and answers
`GET /api/webtools` (the status) and `POST /api/webtools/generate`,
`/api/webtools/start`, `/api/webtools/stop`.

`Status.Proxy` and the fetch error share one redaction:
`webtool.ProxyDisplay` (3.5.0, exported from `tool/web` for its second
consumer), so the tool's error and the dashboard's JSON cannot disagree
about what a redacted proxy URL looks like.

## decisions

- **Generated, not shipped as a tarball.** The bundle's bytes live in
  the binary behind `//go:embed`. A setup with no network, a bounded
  file set, and a `differ` check against the template without a hash
  store. The cost is named: the bundle moves only with a rig release,
  which is the point — the operator's hand edits drifted from a
  rebuildable shape, and only `docker compose up -d --build` applies a
  conf edit at all (`docker restart` keeps the baked copy, measured
  twice in both directions).
- **The password is the machine's, and it is reused.** First setup
  generates 32 hex from `crypto/rand` (128 bits, the length the
  operator's bundle already carries); a later setup reads the existing
  `BasicAuth` line so a re-run is byte-identical and `start` never
  rotates a credential the containers already hold. A user-supplied
  password must match `[A-Za-z0-9._-]{16,128}`: the BasicAuth password
  is **unquoted** in tinyproxy's parser (a quote, a space or a `#`
  changes the parsed password — `401` on every request, measured), the
  same bytes land in `settings.json`, and 16 is the entropy floor
  under the generated 32. Refuse anything else naming the rule.
- **Quoting asymmetry, written into the file.** `BasicAuth rig <pw>`
  unquoted, `BasicAuthRealm "proxy"` quoted: unquoted realm is
  `Syntax error on line N` and tinyproxy refuses to start (measured
  live: nine restarts before the isolated build caught it). The
  generated conf carries the comment, because a user editing the file
  would otherwise meet the same trap.
- **Modes travel with the bundle.** The conf is `0600` on the host and
  `COPY --chown=tinyproxy:tinyproxy --chmod=600` inside the image.
  `COPY` preserves the host mode, the container runs as uid 100, and a
  root-owned `0600` copy reads `Could not open config file` → exit 70
  → a restart loop that `docker ps` still reports as `Up`. Tightening
  the mode and `--chown` must travel together; the template ships both.
- **BasicAuth on from the first byte, realm renamed.** Before auth, any
  container on the machine could relay through the proxy (measured:
  `200` from a same-network client), and `Allow` cannot bind it because
  cross-subnet clients arrive as the bridge gateway, inside the `Allow`
  line's own `/24`. The stock realm `Tinyproxy` advertises the software
  in the `407` challenge; `proxy` does not.
- **The start order is fixed.** `docker volume create` → the chown
  helper (`docker run --rm --user 0:0 --entrypoint chown -v
  rig-valkey-data:/data alpine:3.23 -R 999:1000 /data`) → `docker
  compose up -d --build`. Named volumes arrive root-owned; valkey runs
  as uid 999 with `read_only`, so a late chown means a crash-loop
  before the first write (the operator's volume was pre-chowned by
  hand, which is not a repeatable step). `--build` is not optional:
  without it a template change keeps running the old baked config.
- **Names and one subnet.** The compose file carries `name:` for the
  network and both volumes, so the bundle does not depend on the
  directory's basename and the chown helper can name the volume. One
  subnet constant (172.30.30.0/24) feeds the compose `ipam`, the proxy
  `Allow` line, and the limiter's `pass_ip` (that subnet's `.1`
  gateway, the address SearXNG sees for host traffic, measured; relayed
  container traffic arrives as the relay's own IP and is deliberately
  not pass-listed). A claimed subnet or port surfaces as docker's own
  error, verbatim.
- **No clocks.** The runner honors the caller's ctx — the request's
  write ctx in the dashboard, the command's ctx in the frontends —
  and the package adds no timeout or poll of its own. Output is capped
  at `logTailBytes`-sized `maxRunBytes` (64 KiB, the number the
  dashboard's log tail already carries; a full `docker info` is a
  fraction of it and a compose build log is the tail case) and the
  truncation says so.
- **Deny by default on the settings write.** `config.SetKey` refuses a
  key outside `knownSettings`, naming the list, keeps the rest of the
  document byte-shaped through the same marshal, writes `0600` and
  renames over. `SetTheme` becomes a call to it: one writer, one shape,
  and the theme behavior stays tested where it was.
- **Absence is a fact, not an error.** `Status` reports `docker` not on
  PATH, a missing bundle, a stack that is down, as fields; it errors
  only on real I/O failure. `Start` and `Stop` fail closed: `docker`
  present and unverified is refused, not assumed. A missing `Tools`
  wiring answers `501` on the dashboard and an error on the command,
  naming that the root did not wire it: it is the build's wiring, not
  a passing outage, so it is not `503`.
- **On Linux, the bundle carries the firewall.** Fetch pins the
  address it checked only when it dials direct (`pinnedDial`); through
  the proxy, tinyproxy resolves the name again, and the deny-list
  matches strings, so a name that answers public to rig and private to
  the proxy reaches the LAN (DNS rebinding), and a `CONNECT` tunnel
  never meets a `://` rule. The operator's hand-built bundle closed
  that with `DOCKER-USER` rules from a root systemd unit; the
  generated one carries them as a service: `firewall/`, on the host
  network with `NET_ADMIN` (every other capability dropped,
  `read_only`, `no-new-privileges`), writes the drops at the top of
  `DOCKER-USER` in order (the subnet's own traffic returns first — a
  host with `br_netfilter` sends searxng-to-valkey through the chain —
  then replies to established flows, then the private ranges), and
  its healthcheck re-checks every rule. tinyproxy and searxng wait for
  it to be healthy; `restart: unless-stopped` brings it back with the
  daemon, so the rules return after a reboot with no unit and no
  sudo. A daemon with no `DOCKER-USER` chain (Docker Desktop on Linux,
  the nftables backend) leaves it unhealthy and the proxy down. The
  file set is Linux only (`WithGOOS`, default `runtime.GOOS`): Docker
  Desktop's daemon lives in a VM this is not measured against, so
  there the deny-list's named limits stand. The compose override is a
  second file, not a slot in the first, so the common bundle is the
  same bytes everywhere and every compose call passes both on
  Linux. The script was measured in an isolated network namespace
  (no chain: refused; applied: ordered above Docker's `RETURN`;
  re-applied: no duplicate; a rule deleted: unhealthy, then healed).
- **No remove verb yet.** See non-goals: the destructive verb needs its
  own named cases, and `stop` covers the everyday need.

## testing

Fake `Runner` keyed by argv, one temp `home` per case, no docker needed
and no network beyond `httptest`:

- `Start` runs volume create, then the chown helper, then `up -d
  --build`, in that order (the fake records argv)
- `Stop` runs `compose stop` and refuses when the bundle is absent
- the rendered conf holds exactly one `BasicAuth` line with the given
  password, the quoted realm, and no template slot left anywhere in the
  file set
- `Generate` writes the whole file set, `0600` on the tinyproxy conf and
  `0644` on the rest, and the generated Dockerfile line carries
  `--chown=tinyproxy:tinyproxy --chmod=600`
- `Generate` is idempotent: the second call reuses the password from
  the existing conf, writes nothing, and says so
- `Generate` refuses when a present file differs from the render,
  naming it, and writes when `force` is set
- a user password outside `[A-Za-z0-9._-]{16,128}` is refused, naming
  the rule; `setup` with no password generates 32 hex
- the secret key in `settings.yml` is 40 hex and generated, not copied
  from the template
- `Generate` writes `webFetchProxy` through the `Setting` seam with the
  credentials, and refuses the write when the seam reports an error,
  naming the key
- `Status` names `docker` missing, lists `missing` and `differ` sorted,
  and keeps the password out of `proxy` while naming user and host
- `Status` reports `ok` for the daemon and the running services when
  the fake answers, and names the daemon's voice when it does not
- `Start` refuses when `docker` is absent and when the daemon does not
  answer, naming the reason, and never reaches compose
- on Linux the bundle is ten files, the firewall's three among them,
  and `up`, `ps` and `stop` all pass the override; a desktop bundle is
  the seven common files and passes none
- the firewall's script returns the subnet's own traffic before the
  drops, names both iptables backends, and the override runs it on the
  host network with `NET_ADMIN`, its healthcheck the gate tinyproxy and
  searxng wait on
- the runner caps its output loudly at the bound
- `config.SetKey`: refuses an unknown key naming the known ones, keeps
  the other keys, creates the file when absent, writes over a `null`
  document, writes `0600`, keeps `theme` working through it
- `/webtools` bare prints the status lines; `setup` writes and names
  the ports; `start` and `stop` cross to their seams; an unknown verb
  refuses naming the four; `setup force` crosses `force` to the seam;
  no reply prints the password; a missing seam names the root that
  should have wired it
- the dashboard: `GET /api/webtools` answers the status JSON from a
  fake `Tools`; `POST /api/webtools/generate`, `/start`, `/stop` cross
  to the verbs; unwired answers `501`; the same-origin and token gates
  apply; an over-cap body answers `400`
- `rig webtools` with no arguments prints usage naming the verbs;
  `rig webtools status` runs without a model row or an endpoint (the
  `-update` path is the precedent)
- `specs/FREEZE.txt` carries the new `webtools/` line, and the freeze
  gate passes on the whole diff

## scope

New leaf package `webtools/` (four Go files plus the embedded file set
and its `PACKAGE.md`), `config/key.go` with `SetTheme` moved onto
`SetKey`, `command/webtools.go` plus four `Env` fields,
`frontend/web/webtools.go` plus four routes plus `WithTools` plus the
update-page panel, `cmd/rig/webtools.go` plus the `webtools` subcommand
check in `main.go` beside `serve` and `decision`, one `FREEZE.txt`
line, and the docs that must move with it (`README.md`'s command table,
`docs/SETUP.md`, `CHANGELOG.md`). `tool/web` grows one exported
`ProxyDisplay`. `core/` and `loop/` are untouched; the loop's bytes do
not move.
