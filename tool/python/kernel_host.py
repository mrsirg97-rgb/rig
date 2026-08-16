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

Deliberately not jupyter_client/ZMQ: that pulls a much larger dependency tree
for features (multi-client, heartbeat, side channels) a single local agent
never uses. Embedded InteractiveShell keeps magics and rich tracebacks.
"""
import io
import json
import os
import re
import sys
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


def run(code: str) -> dict:
    out, err = io.StringIO(), io.StringIO()
    result_repr, error = None, None
    try:
        with redirect_stdout(out), redirect_stderr(err):
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
    return {
        "ok": error is None,
        "out": _clip(out.getvalue()),
        "err": _clip(err.getvalue()),
        "result": _clip(result_repr) if result_repr else None,
        "error": _clip(error) if error else None,
    }


def main() -> None:
    real_stdout = sys.stdout
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except Exception as e:
            print(json.dumps({"id": None, "ok": False, "error": f"bad request: {e}"}),
                  file=real_stdout, flush=True)
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
            resp = run(req.get("code", ""))
        resp["id"] = rid
        print(json.dumps(resp), file=real_stdout, flush=True)


if __name__ == "__main__":
    main()
