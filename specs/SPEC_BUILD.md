# rig: the build surface (the Makefile, the CI job)

The build surface, boring on purpose: one Makefile with six named targets,
one Linux CI job, and one line of documented install path. The Makefile is
CI's vocabulary and the operator's shorthand, nothing more. No Go code
changes — except the gofmt drift that `fmt-check` catches on day one, and
the single formatting commit that pays it.

The baseline is 0.4.0 (main at ce18671, the plugins merge included). The
invariant: zero semantic Go diff on this branch — the only Go delta is the
formatting (three files, gofmt's own alignment), and `core/` and `loop/`
keep the freeze as behavior (`core/seam_test.go` picks up a comment
alignment in the drift commit; the exception is named, not hidden).

## goals

- A `Makefile` with exactly six targets, all `.PHONY`, recipes tab-indented:
  - `build` — `go build -o bin/rig ./cmd/rig`. The binary lands in a
    repo-local `bin/`, gitignored.
  - `install` — `build`, then copy `bin/rig` into `$(BINDIR)`.
  - `test` — `go vet ./... && go test ./...`.
  - `fmt` — `gofmt -w` over the repo.
  - `fmt-check` — `gofmt -l`; fails loud when anything is unformatted, the
    files listed in the output.
  - `run` — `build`, then exec `bin/rig`.
  No other targets: no `all`, no `clean`, no `release`.
- The `install` destination chain: `$(GOBIN)` when `go env GOBIN` is set,
  else `~/.local/bin`; `BINDIR=...` overrides.
- The CI job (`.github/workflows/ci.yml`): on `pull_request` and `push` to
  `main`, one Linux job — checkout, `setup-go` with
  `go-version-file: go.mod` and the module cache, then `make test` and
  `make fmt-check`.
- The drift commit: one commit on this branch runs `make fmt` and carries
  only the formatting delta, so `fmt-check` goes green and stays the gate.
- The documented install path:
  `go install github.com/mrsirg97-rgb/rig/cmd/rig@latest` (README
  quickstart, `docs/SETUP.md` build section).

## non-goals (the point)

- No install script, no `curl | sh`: the binary is the installer, and
  `go install .../cmd/rig@latest` is the documented path.
- No packaging: no tarballs, no deb/rpm, no installer artifacts —
  post-open-source work, if ever.
- No cross-compile, no matrix, no Windows/macOS CI: one job, linux only.
- No release workflow: a tag is a release decision (the `Version` freeze),
  not a pipeline.
- No version stamping via `ldflags`: `Version` is a spec-pinned const
  (`cmd/rig`, held by `TestVersionIsTheFreeze`), and a stamped version
  would fight it — the stamp is a code change wearing an env's clothes.
- Nothing in the Makefile beyond the six targets: no macros, no variables
  beyond the `GOBIN`/`BINDIR` chain. The Makefile is CI's vocabulary and
  the operator's shorthand, nothing more.
- No Go code changes beyond `gofmt`: no import, no signature, no behavior.

## layout

```
Makefile                     the six targets (this spec)
.github/workflows/ci.yml     the one job
specs/SPEC_BUILD.md          this file
.gitignore                   + /bin
README.md                    quickstart: the go install line
docs/SETUP.md                build: the go install line
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
	cp bin/rig $(BINDIR)/

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
the copy: the default destination is created on first install, and a
directory the operator already made is taken as-is.

### 3. fmt-check is the gate

`gofmt -l .` empty is green; non-empty is a failure that lists the
offending files in its output — a red you can act on from the first line,
not an exit code you must decode. CI runs it after `make test` on every
pull request and every push to main, so the drift this branch pays is born
formatted: after the drift commit, a PR that lands unformatted code is red
in the job, not a remark in the review.

### 4. The CI job

One job, `ubuntu-latest`, on `pull_request` and `push` to `main`. The
steps: `actions/checkout`, `actions/setup-go` with
`go-version-file: go.mod` (the `go 1.26.6` module line is the toolchain's
single source of truth — the workflow names no version) and the module
cache, then `make test`, then `make fmt-check`. No artifacts, no uploads,
no secrets, no matrix.

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
- The full suite green before the PR.

## scope

What this is not: the non-goals above, each in its line. The loop, the
stores, the wire: zero diff. The runtime stays 0.4.0; the build surface is
new, not a change.
