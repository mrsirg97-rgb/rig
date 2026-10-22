#!/usr/bin/env python3
"""Persistent IPython shell driven over a JSON-lines protocol.

One process per agent session. State (variables, imports, defs) persists across
calls, which is the whole point -- the model pays the setup cost once and then
does cheap incremental work against live objects.

Protocol: one JSON object per line on stdin, one per line on stdout.
  in : {"id": "1", "code": "x = 2"}      execute
       {"id": "2", "cmd": "reset"}       fresh namespace
       {"id": "3", "cmd": "vars"}        summarise the namespace
  out: {"id": "1", "ok": true, "out": "...", "result": "...", "error": null}
       {"id": "1", "stream": "..."}      a cell's stdout/stderr, as it drains
       (a stream line's text is also collected into the final reply's "out"/"err")

Deliberately not jupyter_client/ZMQ: that pulls a much larger dependency tree
for features (multi-client, heartbeat, side channels) a single local agent
never uses. Embedded InteractiveShell keeps magics and rich tracebacks.
"""
import io
import json
import os
import re
import sys
import threading
import traceback
from contextlib import redirect_stderr, redirect_stdout

os.environ.setdefault("MPLBACKEND", "Agg")  # never try to open a window

from IPython.core.interactiveshell import InteractiveShell  # noqa: E402

MAX_OUT = 16000  # chars per stream; the model does not need a megabyte of logs

_ANSI = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")


def _clip(s: str) -> str:
    s = _ANSI.sub("", s)  # colour codes are pure token cost to an LLM reader
    if len(s) <= MAX_OUT:
        return s
    head, tail = s[: MAX_OUT // 2], s[-MAX_OUT // 2 :]
    return f"{head}\n... [{len(s) - MAX_OUT} chars elided] ...\n{tail}"


def new_shell() -> InteractiveShell:
    InteractiveShell.clear_instance()
    sh = InteractiveShell.instance()
    sh.display_formatter.active_types = ["text/plain"]  # no HTML/image payloads
    sh.colors = "NoColor"
    sh.InteractiveTB.set_mode(mode="Minimal")  # error text already goes in "error"
    return sh


shell = new_shell()


def describe_vars() -> str:
    """User-defined names only -- IPython's own machinery is noise here."""
    ns = shell.user_ns
    hidden = set(shell.user_ns_hidden) | {"In", "Out", "get_ipython", "exit", "quit", "open"}
    rows = []
    for k, v in ns.items():
        if k.startswith("_") or k in hidden:
            continue
        t = type(v).__name__
        try:
            extra = f" len={len(v)}" if hasattr(v, "__len__") else ""
        except Exception:
            extra = ""
        rows.append(f"{k}: {t}{extra}")
    return "\n".join(sorted(rows)) or "(empty)"


def _pump(fd: int, chunks: list, rid: str, emit) -> None:
    while True:
        raw = os.read(fd, 4096)
        if not raw:
            os.close(fd)
            return
        text = raw.decode("utf-8", "replace")
        chunks.append(text)
        emit({"id": rid, "stream": _clip(text)})


def run(code: str, rid: str, emit) -> dict:
    out_chunks, err_chunks = [], []
    result_repr, error = None, None
    out_r, out_w = os.pipe()
    err_r, err_w = os.pipe()
    out_wfile = os.fdopen(out_w, "w", buffering=1, encoding="utf-8", errors="replace")
    err_wfile = os.fdopen(err_w, "w", buffering=1, encoding="utf-8", errors="replace")
    saved1, saved2 = os.dup(1), os.dup(2)
    pumps = [threading.Thread(target=_pump, args=(out_r, out_chunks, rid, emit)),
             threading.Thread(target=_pump, args=(err_r, err_chunks, rid, emit))]
    try:
        for p in pumps:
            p.start()
        os.dup2(out_w, 1)
        os.dup2(err_w, 2)
        with redirect_stdout(out_wfile), redirect_stderr(err_wfile):
            res = shell.run_cell(code, store_history=True, silent=False)
        if res.error_before_exec is not None:
            error = "".join(traceback.format_exception_only(type(res.error_before_exec),
                                                           res.error_before_exec)).strip()
        elif res.error_in_exec is not None:
            error = "".join(traceback.format_exception_only(type(res.error_in_exec),
                                                           res.error_in_exec)).strip()
        elif res.result is not None:
            try:
                result_repr = repr(res.result)
            except Exception:
                result_repr = "<unreprable>"
    except Exception:
        error = traceback.format_exc()
    finally:
        os.dup2(saved1, 1)
        os.dup2(saved2, 2)
        os.close(saved1)
        os.close(saved2)
        out_wfile.close()
        err_wfile.close()
        for p in pumps:
            p.join()
    return {
        "ok": error is None,
        "out": _clip("".join(out_chunks)),
        "err": _clip("".join(err_chunks)),
        "result": _clip(result_repr) if result_repr else None,
        "error": _clip(error) if error else None,
    }


def main() -> None:
    proto = os.fdopen(os.dup(1), "w", buffering=1, encoding="utf-8", errors="replace")
    proto_lock = threading.Lock()

    def emit(obj: dict) -> None:
        line = json.dumps(obj) + "\n"
        with proto_lock:
            proto.write(line)
            proto.flush()

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except Exception as e:
            emit({"id": None, "ok": False, "error": f"bad request: {e}"})
            continue

        rid, cmd = req.get("id"), req.get("cmd")
        if cmd == "reset":
            global shell
            shell = new_shell()
            resp = {"ok": True, "out": "kernel reset; namespace is empty", "err": "",
                    "result": None, "error": None}
        elif cmd == "vars":
            resp = {"ok": True, "out": describe_vars(), "err": "", "result": None, "error": None}
        elif cmd == "ping":
            resp = {"ok": True, "out": "pong", "err": "", "result": None, "error": None}
        else:
            resp = run(req.get("code", ""), rid, emit)
        resp["id"] = rid
        emit(resp)


if __name__ == "__main__":
    main()
