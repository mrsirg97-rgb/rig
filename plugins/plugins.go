// Package plugins is the python plugin surface (SPEC_PLUGINS): one file
// under the rig home's plugins/ directory, one tool per file, the name
// the filename stem. Discovery runs at startup through the shared
// python kernel — the same persistent kernel as tool/python, one
// process, the namespace shared — and the loaded tools register on the
// existing Tool seam, indistinguishable from a native tool on the wire.
//
// Stdlib plus core and tool/python (the kernel seam), nothing else: the
// leaf discovers and wraps; the root (cmd/rig) wires.
package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
	pythontool "github.com/mrsirg97-rgb/rig/tool/python"
)

// defaultTimeoutMs is the cell's budget, the python tool's own
// (SPEC_PLUGINS 3): the timeout starts only after the kernel slot is
// taken, so a queued call is never charged queue time.
const defaultTimeoutMs = 120000

// Report is one plugin file's discovery outcome (SPEC_PLUGINS 2): the
// name (the filename stem), the file, and — when loaded — the
// description and schema the wire carries; when skipped — the reason
// (the kernel's voice, verbatim).
type Report struct {
	Name        string
	File        string
	Description string
	Schema      json.RawMessage
	Skipped     bool
	Reason      string
}

// Kernel is the shared-kernel seam (SPEC_PLUGINS 1, 3): one code cell,
// the host's raw reply. tool/python's Tool implements it (Run); the
// tests stand in with a fake, no python required.
type Kernel interface {
	Run(ctx context.Context, code string, timeoutMs int) (pythontool.Reply, error)
}

// wireReport is the kernel's per-file row, as printed by the discovery
// cell (the JSON list on the cell's stdout).
type wireReport struct {
	Name        string          `json:"name"`
	File        string          `json:"file"`
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Error       string          `json:"error"`
}

// Discover imports every file through the kernel and reports each, in
// file order (SPEC_PLUGINS 2). A kernel-level failure (the call gave
// up, the kernel died, the report is not the JSON list) is the error;
// a per-file failure is a skipped report, never the error — a broken
// plugin must not brick the harness.
func Discover(ctx context.Context, k Kernel, files []string) ([]Report, error) {
	reply, err := k.Run(ctx, discoveryCell(files), defaultTimeoutMs)
	if err != nil {
		return nil, err
	}
	if !reply.Ok {
		return nil, fmt.Errorf("discovery: %s", errorTail(reply))
	}
	var out []wireReport
	if reply.Out != nil && strings.TrimSpace(*reply.Out) != "" {
		if err := json.Unmarshal([]byte(strings.TrimSpace(*reply.Out)), &out); err != nil {
			return nil, fmt.Errorf("discovery: the kernel's report is not a JSON list: %v", err)
		}
	}
	reports := make([]Report, 0, len(out))
	for _, w := range out {
		reports = append(reports, Report{
			Name:        w.Name,
			File:        w.File,
			Description: w.Description,
			Schema:      w.Schema,
			Skipped:     !w.OK,
			Reason:      w.Error,
		})
	}
	return reports, nil
}

// discoveryCell is the kernel-side discovery (SPEC_PLUGINS 2): import
// each file by path, validate the three names, keep the module where
// the kernel can reach it (the user namespace's __rig_plugins__, and
// sys.modules under the stem), and print one JSON report line. The
// per-file outcome is the kernel's own voice: the missing piece names
// the field, the import failure carries the exception.
func discoveryCell(files []string) string {
	paths, _ := json.Marshal(files) // a JSON array of strings; the error is unreachable
	return `import importlib.util as _rig_iu, json as _rig_j, os as _rig_os, sys as _rig_sys
__rig_plugins__ = {}
_rig_report = []
for _rig_p in _rig_j.loads('` + pyLiteral(string(paths)) + `'):
    _rig_n = _rig_os.path.basename(_rig_p)[:-3]
    _rig_e = {"name": _rig_n, "file": _rig_p}
    try:
        if not _rig_n:
            raise ValueError("empty name (the filename stem)")
        _rig_s = _rig_iu.spec_from_file_location(_rig_n, _rig_p)
        _rig_m = _rig_iu.module_from_spec(_rig_s)
        _rig_s.loader.exec_module(_rig_m)
        _rig_miss = [f for f in ("DESCRIPTION", "SCHEMA", "run") if not hasattr(_rig_m, f)]
        if _rig_miss:
            raise TypeError("missing " + ", ".join(_rig_miss))
        if not isinstance(_rig_m.DESCRIPTION, str):
            raise TypeError("DESCRIPTION must be a str")
        if not isinstance(_rig_m.SCHEMA, dict):
            raise TypeError("SCHEMA must be a dict")
        if not callable(_rig_m.run):
            raise TypeError("run must be callable")
        _rig_sys.modules.setdefault(_rig_n, _rig_m)
        __rig_plugins__[_rig_n] = _rig_m
        _rig_e["ok"] = True
        _rig_e["description"] = _rig_m.DESCRIPTION
        _rig_e["schema"] = _rig_m.SCHEMA
    except Exception as _rig_ex:
        _rig_e["ok"] = False
        _rig_e["error"] = type(_rig_ex).__name__ + ": " + str(_rig_ex)
    _rig_report.append(_rig_e)
print(_rig_j.dumps(_rig_report))
`
}

// Tool is one loaded plugin on the Tool seam (SPEC_PLUGINS 2, 3): the
// name is the filename stem, the description and schema are the file's
// verbatim (the wire's three), the call rides the shared kernel.
type Tool struct {
	name        string
	description string
	file        string
	schema      json.RawMessage
	k           Kernel
}

var _ core.Tool = (*Tool)(nil) // the seam is compile-time enforced

// New wraps one discovery report as the seam's tool.
func New(name, description, file string, schema json.RawMessage, k Kernel) *Tool {
	return &Tool{name: name, description: description, file: file, schema: schema, k: k}
}

// File is the plugin file's path, for the /plugins listing (SPEC_PLUGINS 4).
func (t *Tool) File() string { return t.file }

// Name implements core.Tool: the filename stem.
func (t *Tool) Name() string { return t.name }

// Description implements core.Tool: the file's DESCRIPTION, verbatim.
func (t *Tool) Description() string { return t.description }

// Schema implements core.Tool: the file's SCHEMA, verbatim.
func (t *Tool) Schema() json.RawMessage { return t.schema }

// Exec is the call (SPEC_PLUGINS 3): the kernel invokes the module's
// run with the args dict, the return prints into the result, and an
// exception is a tool error carrying the traceback tail — the kernel
// stays alive, as it is the model's python kernel too.
func (t *Tool) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	argsJSON, err := compactJSON(args)
	if err != nil {
		return "", err
	}
	reply, err := t.k.Run(ctx, callCell(t.name, argsJSON), defaultTimeoutMs)
	if err != nil {
		return "", err // the call gave up (ctx); the kernel is untouched
	}
	if !reply.Ok {
		msg := t.name + ": " + errorTail(reply)
		out := ""
		if reply.Out != nil {
			out = strings.TrimSuffix(*reply.Out, "\n")
		}
		if out != "" {
			return out + "\n" + msg, errors.New(msg) // the partial output rides along
		}
		return "", errors.New(msg)
	}
	out := ""
	if reply.Out != nil {
		out = strings.TrimSuffix(*reply.Out, "\n") // print's own newline, dropped
	}
	return out, nil
}

// callCell is the call (SPEC_PLUGINS 3): the module's run with the args
// dict, the return printed. The kernel already has the module (the
// discovery imported it); the call adds nothing — json is stdlib, in
// sys.modules.
func callCell(name, argsJSON string) string {
	named, _ := json.Marshal(name) // a JSON string literal; a valid python literal
	return "import json as _rig_j\nprint(__rig_plugins__[" + string(named) + "].run(_rig_j.loads('" + pyLiteral(argsJSON) + "')))"
}

// compactJSON re-marshals the model's args compactly: the cell carries
// them as one embedded literal, and compact JSON has no raw newlines —
// the embedding (pyLiteral) is total on it.
func compactJSON(raw json.RawMessage) (string, error) {
	var v any
	if len(raw) == 0 {
		return "null", nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", fmt.Errorf("the args are not JSON: %v", err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// pyLiteral embeds s in a single-quoted python string literal: JSON
// text has no raw newlines, no double-quote collisions; the only live
// characters are the backslash and the single quote.
func pyLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

// errorTail is the reply's error voice: the exception's type and
// message (the host's format_exception_only tail), else the stderr
// stream, else a named gap.
func errorTail(r pythontool.Reply) string {
	if r.Error != nil && *r.Error != "" {
		return *r.Error
	}
	if r.Err != nil && *r.Err != "" {
		return *r.Err
	}
	return "(the kernel reported a failure with no error text)"
}
