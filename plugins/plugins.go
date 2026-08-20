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

const defaultTimeoutMs = 120000

type Report struct {
	Name        string
	File        string
	Description string
	Schema      json.RawMessage
	Skipped     bool
	Reason      string
}

type Kernel interface {
	Run(ctx context.Context, code string, timeoutMs int) (pythontool.Reply, error)
}

type wireReport struct {
	Name        string          `json:"name"`
	File        string          `json:"file"`
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Error       string          `json:"error"`
}

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

func discoveryCell(files []string) string {
	paths, _ := json.Marshal(files)
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

type Tool struct {
	name        string
	description string
	file        string
	schema      json.RawMessage
	k           Kernel
}

var _ core.Tool = (*Tool)(nil)

func New(name, description, file string, schema json.RawMessage, k Kernel) *Tool {
	return &Tool{name: name, description: description, file: file, schema: schema, k: k}
}

func (t *Tool) File() string { return t.file }

func (t *Tool) Name() string { return t.name }

func (t *Tool) Description() string { return t.description }

func (t *Tool) Schema() json.RawMessage { return t.schema }

func (t *Tool) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	argsJSON, err := compactJSON(args)
	if err != nil {
		return "", err
	}
	reply, err := t.k.Run(ctx, callCell(t.name, argsJSON), defaultTimeoutMs)
	if err != nil {
		return "", err
	}
	if !reply.Ok {
		msg := t.name + ": " + errorTail(reply)
		out := ""
		if reply.Out != nil {
			out = strings.TrimSuffix(*reply.Out, "\n")
		}
		if out != "" {
			return out + "\n" + msg, errors.New(msg)
		}
		return "", errors.New(msg)
	}
	out := ""
	if reply.Out != nil {
		out = strings.TrimSuffix(*reply.Out, "\n")
	}
	return out, nil
}

func callCell(name, argsJSON string) string {
	named, _ := json.Marshal(name)
	return "import json as _rig_j\nprint(__rig_plugins__[" + string(named) + "].run(_rig_j.loads('" + pyLiteral(argsJSON) + "')))"
}

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

func pyLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

func errorTail(r pythontool.Reply) string {
	if r.Error != nil && *r.Error != "" {
		return *r.Error
	}
	if r.Err != nil && *r.Err != "" {
		return *r.Err
	}
	return "(the kernel reported a failure with no error text)"
}
