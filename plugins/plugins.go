package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/core"
	pythontool "github.com/mrsirg97-rgb/rig/v2/tool/python"
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

func Discover(ctx context.Context, k Kernel, files []string, c Contract) ([]Report, error) {
	reply, err := k.Run(ctx, discoveryCell(files, c), defaultTimeoutMs)
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

func DiscoverChecked(ctx context.Context, k Kernel, files []string, natives map[string]bool, c Contract) ([]Report, error) {
	eligible := make([]string, 0, len(files))
	skipped := make(map[string]Report)
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		switch {
		case !PluginNameRe.MatchString(name):
			skipped[file] = Report{Name: name, File: file, Skipped: true, Reason: "invalid " + c.Kind + " name (want lowercase, digits and underscores, a leading letter)"}
		case natives[name]:
			return nil, nameCollisionError{name: name, file: file}
		default:
			eligible = append(eligible, file)
		}
	}
	loaded := make(map[string]Report)
	if len(eligible) > 0 {
		reports, err := Discover(ctx, k, eligible, c)
		if err != nil {
			return nil, err
		}
		for _, report := range reports {
			loaded[report.File] = report
		}
	}
	out := make([]Report, 0, len(files))
	for _, file := range files {
		if report, ok := skipped[file]; ok {
			out = append(out, report)
		} else if report, ok := loaded[file]; ok {
			out = append(out, report)
		}
	}
	return out, nil
}

func discoveryCell(files []string, c Contract) string {
	paths, _ := json.Marshal(files)
	names, _ := json.Marshal(attrNames(c))
	attrs, _ := json.Marshal(attrPairs(c))
	registry := c.registry()
	description, schema := "", ""
	for _, a := range c.Attrs {
		switch a.Kind {
		case "str":
			description = a.Name
		case "dict":
			schema = a.Name
		}
	}
	reads := ""
	if description != "" {
		reads += fmt.Sprintf("        _rig_e[\"description\"] = _rig_m.%s\n", description)
	}
	if schema != "" {
		reads += fmt.Sprintf("        _rig_e[\"schema\"] = _rig_m.%s\n", schema)
	}
	return `import importlib.util as _rig_iu, json as _rig_j, os as _rig_os, sys as _rig_sys
_rig_next = {}
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
        _rig_miss = [f for f in ` + string(names) + ` if not hasattr(_rig_m, f)]
        if _rig_miss:
            raise TypeError("missing " + ", ".join(_rig_miss))
        for _f, _k in ` + string(attrs) + `:
            _v = getattr(_rig_m, _f)
            if (_k == "str" and not isinstance(_v, str)) or (_k == "dict" and not isinstance(_v, dict)) or (_k == "callable" and not callable(_v)):
                raise TypeError(_f + " must be a " + _k)
        _rig_next[_rig_n] = _rig_m
        _rig_e["ok"] = True
` + reads + `    except Exception as _rig_ex:
        _rig_e["ok"] = False
        _rig_e["error"] = type(_rig_ex).__name__ + ": " + str(_rig_ex)
    _rig_report.append(_rig_e)
for _rig_n in set(globals().get("` + registry + `", {})) - set(_rig_next):
    _rig_sys.modules.pop(_rig_n, None)
for _rig_n, _rig_m in _rig_next.items():
    _rig_sys.modules[_rig_n] = _rig_m
` + registry + ` = _rig_next
print(_rig_j.dumps(_rig_report))
`
}

func attrNames(c Contract) []string {
	out := make([]string, 0, len(c.Attrs))
	for _, a := range c.Attrs {
		out = append(out, a.Name)
	}
	return out
}

func attrPairs(c Contract) [][2]string {
	out := make([][2]string, 0, len(c.Attrs))
	for _, a := range c.Attrs {
		out = append(out, [2]string{a.Name, a.Kind})
	}
	return out
}

func Invoke(ctx context.Context, k Kernel, kind, name, fn string, timeoutMs int, args ...string) (json.RawMessage, error) {
	reply, err := k.Run(ctx, invokeCell(kind, name, fn, args), timeoutMs)
	if err != nil {
		return nil, err
	}
	if !reply.Ok {
		return nil, fmt.Errorf("%s.%s: %s", name, fn, errorTail(reply))
	}
	out := ""
	if reply.Out != nil {
		out = strings.TrimSpace(*reply.Out)
	}
	if out == "" {
		return nil, fmt.Errorf("%s.%s: the kernel printed nothing", name, fn)
	}
	return json.RawMessage(out), nil
}

func invokeCell(kind, name, fn string, args []string) string {
	payload, _ := json.Marshal(args)
	named, _ := json.Marshal(name)
	fnName, _ := json.Marshal(fn)
	return "import json as _rig_j\nprint(_rig_j.dumps(getattr(__rig_" + kind + "s__[" + string(named) + "], " + string(fnName) + ")(*_rig_j.loads('" + pyLiteral(string(payload)) + "'))))"
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
	if strings.ContainsAny(s, "\n\r") {
		panic("plugins: pyLiteral received a raw line break; the input must be JSON-marshalled")
	}
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
