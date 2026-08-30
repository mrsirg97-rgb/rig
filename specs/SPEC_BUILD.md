# rig: the build surface (the Makefile, the CI job)

The build surface, boring on purpose: one Makefile with six named targets,
one Linux CI job (the PR gate), one release workflow (the tag path), one
POSIX installer, and one static install site. The Makefile is CI's
vocabulary and the operator's shorthand, nothing more. No Go code
changes; except the gofmt drift that `fmt-check` catches on day one, the
single formatting commit that pays it, and the one distribution-round
delta: two Linux-only signal names dropped from the python tool's
signal-name map (`tool/python/python.go`), so the darwin cross-build
compiles; the named cost of the four-target matrix.

The baseline is 0.4.0 (main at ce18671, the plugins merge included). The
invariant: zero semantic Go diff on this branch; the only Go delta is the
formatting (three files, gofmt's own alignment), and `core/` and `loop/`
keep the freeze as behavior (`core/seam_test.go` picks up a comment
alignment in the drift commit; the exception is named, not hidden).

## goals

- A `Makefile` with exactly six targets, all `.PHONY`, recipes tab-indented:
  - `build`; `go build -o bin/rig ./cmd/rig`. The binary lands in a
    repo-local `bin/`, gitignored.
  - `install`; `build`, then copy `bin/rig` into `$(BINDIR)`.
  - `test`; `go vet ./... && go test ./...`.
  - `fmt`; `gofmt -w` over the repo.
  - `fmt-check`; `gofmt -l`; fails loud when anything is unformatted, the
    files listed in the output.
  - `run`; `build`, then exec `bin/rig`.
  No other targets: no `all`, no `clean`, no `release`.
- The `install` destination chain: `$(GOBIN)` when `go env GOBIN` is set,
  else `~/.local/bin`; `BINDIR=...` overrides.
- The CI job (`.github/workflows/ci.yml`): on `pull_request` and `push` to
  `main`, one Linux job; checkout, `setup-go` with
  `go-version-file: go.mod` and the module cache, then `make test`,
  `make fmt-check`, and `shellcheck install.sh`. It stays the PR gate;
  a tag ships through the release workflow, not this one.
- The drift commit: one commit on this branch runs `make fmt` and carries
  only the formatting delta, so `fmt-check` goes green and stays the gate.
- The documented install paths, three (decision 5):
  the installer (`curl -fsSL https://mrsirg97-rgb.github.io/rig/install.sh
  | sh`), the release binary (the same asset the installer fetches,
  downloaded directly), and
  `go install github.com/mrsirg97-rgb/rig/cmd/rig@latest` (README
  quickstart, `docs/SETUP.md` build section).

## non-goals (the point)

- **goreleaser**: a dependency (and its config) for a four-entry matrix
  that one shell loop plus three pinned actions already ship.
- **Homebrew / AUR**: packaging for a package manager is "later, if
  asked"; the installer covers the operator who does not want Go.
- **The version in the asset name**: the asset is `rig_<os>_<arch>`, no
  version, no extension: the download URL carries the version (a release
  download is scoped to its tag), so the name stays a stable reference
  and the checksums.txt is the tie to the tag.
- **Installing with sudo**: never sudo, never `/usr/local`; the installer
  lands in `${RIG_BIN:-$HOME/.local/bin}` with `install -m 0755`, the
  operator's own directory.
- No Windows: the matrix is linux/darwin x amd64/arm64 (the sqlite is
  modernc, pure Go; no cgo toolchains).
- No version stamping via `ldflags`: `Version` is a spec-pinned const
  (`cmd/rig`, held by `TestVersionIsTheFreeze`), and a stamped version
  would fight it; the stamp is a code change wearing an env's clothes.
- Nothing in the Makefile beyond the six targets: no macros, no variables
  beyond the `GOBIN`/`BINDIR` chain. The Makefile is CI's vocabulary and
  the operator's shorthand, nothing more.
- No Go code changes beyond `gofmt` and the one named distribution
  delta: two Linux-only signal names (`SIGSTKFLT`, `SIGPWR`) dropped from
  the python tool's signal-name map so the darwin cross-build compiles:
  no import, no signature, no other behavior.

## layout

```
Makefile                     the six targets (this spec)
.github/workflows/ci.yml     the PR gate: test, fmt-check, shellcheck
.github/workflows/release.yml the tag path: assert, cross-build, attest, release
.github/workflows/pages.yml  the install site, on push to main
install.sh                   the POSIX installer (decision 5)
site/index.html              the one static page (decision 5)
specs/SPEC_BUILD.md          this file
.gitignore                   + /bin
README.md                    quickstart: the three install paths
docs/SETUP.md                build: the three install paths
```

## decisions

### 1. The six targets, verbatim

The Makefile is this, and nothing else:

```make
GOBIN   ?= $(shell go env GOBIN)
BINDIR  ?= $(if $(strip $(GOBIN)),$(GOBIN),$(HOME)/.local/bin)

.PHONY: build install test fmt fmt-check run

build:
	go build -o bin/rig ./cmd/rig

install: build
	mkdir -p $(BINDIR)
	install -m 0755 bin/rig $(BINDIR)/

test:
	go vet ./...
	go test ./...

fmt:
	gofmt -w .

fmt-check:
	@if [ -n "$$(gofmt -l .)" ]; then \
		echo "make fmt-check: unformatted files (run 'make fmt'):" >&2; \
		gofmt -l . >&2; \
		exit 1; \
	fi

run: build
	./bin/rig
```

Each target says one thing and does it: `install` depends on `build`
rather than repeating its recipe; `run` is `build` plus the exec. The
`.PHONY` line covers all six, because every one is a verb, not a file.

### 2. The install destination chain

`$(BINDIR)` resolves in one rule, in order: a `BINDIR=...` on the command
line (or in the environment) wins; else `$(GOBIN)` when `go env GOBIN` is
non-empty; else `$(HOME)/.local/bin`. The Makefile asks `go env` for the
toolchain's own answer rather than guessing at GOPATH. `mkdir -p` precedes
the landing: the default destination is created on first install, and a
directory the operator already made is taken as-is. The landing is
`install -m`, not `cp`: a `cp` over a binary that is running fails with
`ETXTBSY`, while `install(1)` unlinks first; an operator who installs
over a live session keeps it running.

### 3. fmt-check is the gate

`gofmt -l .` empty is green; non-empty is a failure that lists the
offending files in its output; a red you can act on from the first line,
not an exit code you must decode. CI runs it after `make test` on every
pull request and every push to main, so the drift this branch pays is born
formatted: after the drift commit, a PR that lands unformatted code is red
in the job, not a remark in the review.

### 4. The CI job

One job, `ubuntu-latest`, on `pull_request` and `push` to `main`. The
steps: `actions/checkout`, `actions/setup-go` with
`go-version-file: go.mod` (the `go 1.26.6` module line is the toolchain's
single source of truth; the workflow names no version) and the module
cache, then `make test`, then `make fmt-check`, then `shellcheck
install.sh`. No artifacts, no uploads, no secrets, no matrix. This job is
the PR gate; the release workflow runs only on tags.

### 5. Distribution

The tag path, the installer, and the site. The first real tag, `v0.8.0`,
is the live test of the whole path: the release workflow must make it
succeed on the first try, and the `Version` const (`cmd/rig`, "0.8.0") is
the fact the tag is asserted against; a tag that does not match the
const refuses to ship.

**The release workflow** (`.github/workflows/release.yml`, on
`push: tags: ['v*']`). One job, `ubuntu-latest`. First step: build the
binary and assert the tag equals "v" + the `Version` constant; `go build
-o bin/rig ./cmd/rig`, run `./bin/rig -version`, take its second field,
and refuse loud (`exit 1`, naming both) when `$GITHUB_REF_NAME` != `v` +
that field. A mismatch fails the release before any asset is built; a
green assert is the whole path's precondition. Then `CGO_ENABLED=0`
cross-builds linux/darwin x amd64/arm64 with `-trimpath
-ldflags="-s -w"` (the sqlite is modernc, pure Go; no cgo toolchains),
naming the assets `rig_<os>_<arch>`; no extension, no version in the
name, and writes `checksums.txt` (sha256 over the four). Then attests
build provenance with `actions/attest-build-provenance` (pinned by major
tag), and creates the GitHub Release with `gh release create`, the body
extracted from the matching `## [<version>]` CHANGELOG.md section (a
missing section refuses, loud, an empty body). Actions pinned by major
tag (`actions/checkout@v4`, `actions/setup-go@v5`, the attest action).
Permissions minimal and explicit: `contents: write`, `id-token: write`,
`attestations: write`.

**The installer** (`install.sh`, repo root). POSIX sh, under 80 lines, no
bashisms, `set -eu`. Maps `uname -s` / `uname -m` into the asset name
(Linux→linux, Darwin→darwin, x86_64/amd64→amd64, aarch64/arm64→arm64)
and refuses unknown pairs by name. The version: `RIG_VERSION` (env) or the
first argument wins; default resolves the `releases/latest` redirect
(no API call, no JSON parse). Downloads the asset and `checksums.txt`
into a `mktemp` dir (`curl -fsSL`, `wget` fallback), verifies with
`sha256sum` or `shasum -a 256` **before anything moves**, installs with
`install -m 0755` into `${RIG_BIN:-$HOME/.local/bin}`; never sudo, never
`/usr/local`; prints the PATH hint when the bindir is not on PATH, then
runs `rig -version`. Every failure names what it was doing. The ci job
shellchecks it.

**The self-update** (`cmd/rig/update.go`), the binary's own installer
beside `-version`: `rig -update` resolves the latest release the same way
the installer does (the `releases/latest` redirect, no API call), maps
`GOOS`/`GOARCH` into `rig_<os>_<arch>`, downloads `checksums.txt` and the
asset, verifies the sha256 **before anything moves**, and renames a
0755 temp file in the resolved executable's directory over the binary:
atomic on one filesystem, so a running rig keeps its old inode and the
scheduler's next fire gets the new one. A directory that cannot be
written names itself and the sudo line; a platform with no asset and a
build whose `Version` has no release tag each say so rather than
downgrading.

**The site** (`site/`, published by `.github/workflows/pages.yml` on
`push` to `main`). One static page: no build step, no JS framework, no
external assets. The page carries the name, one line on what rig is
(AGENTS.md's overview), the install line `curl -fsSL
https://mrsirg97-rgb.github.io/rig/install.sh | sh`, the `go install
github.com/mrsirg97-rgb/rig/cmd/rig@latest` alternative, and links to the
README, `specs/`, and the latest release. The pages job copies
`install.sh` into the artifact so the site URL serves the same bytes as
the repo root.

## testing

The proof is the CI itself: the workflow green on this PR is the
deliverable's own test. The local checklist, in order:

- `make test` green.
- `make fmt-check` red before the drift commit (the three drifted files
  named), green after it.
- `make install` lands a binary that runs (a live session, `/models`);
  `make install BINDIR=<dir>` lands it in the named directory.
- `make build` leaves `bin/rig` and `git status` clean of it (the `/bin`
  ignore holds).
- `shellcheck install.sh` green.
- `sh install.sh 0.7.0` (a local, pre-tag version) downloads, verifies,
  and runs `rig -version`; an unknown `uname -m` pair and a checksum
  mismatch each fail loud, naming the step.
- The full suite green before the PR.

## scope

What this is not: the non-goals above, each in its line. The loop, the
stores, the wire: zero diff. The runtime stays 0.4.0; the build surface is
new, not a change.
