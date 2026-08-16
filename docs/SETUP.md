# looper setup

looper has **zero third-party dependencies** (`go.mod` carries no require
lines); everything is Go standard library. Setup is fetch, configure, build.

## prerequisites

- **Go** with a toolchain that satisfies `go 1.26.6` in `go.mod`. Any Go ≥
  1.26 works: the toolchain line pulls the newest matching patch automatically
  (`GOTOOLCHAIN=auto` is the default). Verify with `go version`.
- An **OpenAI-compatible** chat-completions endpoint (SSE streaming): a local
  model server, a gateway, or the hosted API. looper speaks the wire protocol
  only; vendor specifics live in your endpoint configuration.

## build

```sh
git clone git@github.com:mrsirg97-rgb/looper.git
cd looper
go build ./cmd/looper     # produces ./looper
./looper --version        # looper 0.1.0
```

Contributors: the gate before any change is

```sh
go test ./... -count=1
go vet ./...
gofmt -l .
```

## configure

There are no config files. Every knob is a flag or an environment variable;
flags win over env, env wins over built-in defaults:

| knob       | flag        | env              | default                              | meaning                                   |
|------------|-------------|------------------|--------------------------------------|-------------------------------------------|
| endpoint   | `--base-url`| `LOOPER_BASE_URL`| `http://127.0.0.1:8090/v1`           | OpenAI-compatible base URL (the worker swap) |
| model      | `--model`   | `LOOPER_MODEL`   | `local`                              | model name sent per request               |
| system     | `--system`  | `LOOPER_SYSTEM`  | looper's default system prompt       | context-policy seed                       |
| allow-list | `--allow`   | `LOOPER_ALLOW`   | `bash,read,write,edit,ls,find,grep,todo,rem,scheduler,python,web_search,web_fetch` | tools permitted to execute                |
| bound      | `--retries` | `LOOPER_RETRIES` | `3`                                  | repetition bound on identical failing calls (see below) |
| python kernel |           | `LOOPER_PYTHON`  | pane's shared venv (the default interpreter) | interpreter for the python kernel; an explicit choice skips the lazy venv bootstrap |
| web search |           | `LOOPER_SEARXNG_URL` | `http://127.0.0.1:8888`        | the SearXNG instance (the web-tools compose) |
| web fetch  |           | `LOOPER_WEB_FETCH_PROXY` | `http://127.0.0.1:8889`    | egress proxy for web_fetch; set empty = direct |
| extraction |           | `LOOPER_TRAFILATURA` | shared venv, then PATH      | the trafilatura binary; a path, or empty = the stdlib text pass carries |

**On `LOOPER_WEB_FETCH_PROXY` and `LOOPER_TRAFILATURA`** — "set empty" means
the variable is present but empty: that is an explicit choice (direct
egress / no trafilatura), while an unset variable takes the compose
default. Presence is the signal, the value is the choice.

**On `LOOPER_RETRIES`** — read before tuning: the value does **not** permit
silent re-execution. Every tool call executes exactly once; the value bounds
the *model's* re-issuance of an identical failing call (counted across turns,
cleared on success). It is a brake on repetition, not a retry allowance.

**On the allow-list** — it is default-deny below it: any tool not named is
refused at the boundary and the refusal is fed back to the model. The default
permits the thirteen built-in tools because a default-deny CLI would ship a
dead agent; narrow with `--allow read` or similar.

## verify

```sh
./looper --version                 # prints: looper 0.1.0
./looper --base-url $YOUR_EPIT --model $NAME --system "be terse"
```

then type a prompt, Ctrl-C to interrupt (the session ends once the in-flight
step unwinds), Ctrl-D to exit at the prompt. If nothing streams, check the endpoint
first — looper surfaces provider faults verbatim and loudly; it does not hide
them.
