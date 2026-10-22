package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pythontool "github.com/mrsirg97-rgb/rig/v3/tool/python"
)

type fakeKernel struct {
	cells   []string
	replies []pythontool.Reply
	errs    []error
}

func (f *fakeKernel) Run(ctx context.Context, code string, timeoutMs int, onStream func(string)) (pythontool.Reply, error) {
	f.cells = append(f.cells, code)
	i := len(f.cells) - 1
	if i < len(f.errs) && f.errs[i] != nil {
		return pythontool.Reply{}, f.errs[i]
	}
	if i < len(f.replies) {
		return f.replies[i], nil
	}
	return pythontool.Reply{Ok: true, Out: strPtr("")}, nil
}

func strPtr(s string) *string { return &s }

func okReply(out string) pythontool.Reply {
	return pythontool.Reply{Ok: true, Out: strPtr(out)}
}

func errReply(tail, out string) pythontool.Reply {
	r := pythontool.Reply{Ok: false, Error: strPtr(tail)}
	if out != "" {
		r.Out = strPtr(out)
	}
	return r
}

func TestDiscoverParsesTheKernelReport(t *testing.T) {
	report := `
[
  {"name": "echo", "file": "/h/plugins/echo.py", "ok": true, "description": "echoes", "schema": {"type": "object"}},
  {"name": "broken", "file": "/h/plugins/broken.py", "ok": false, "error": "NameError: name 'x' is not defined"}
]`
	k := &fakeKernel{replies: []pythontool.Reply{okReply(report + "\n")}}
	reports, err := Discover(context.Background(), k, []string{"/h/plugins/echo.py", "/h/plugins/broken.py"}, PluginContract)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("reports = %d, want 2: %+v", len(reports), reports)
	}
	loaded := reports[0]
	if loaded.Name != "echo" || loaded.File != "/h/plugins/echo.py" || loaded.Description != "echoes" || loaded.Skipped {
		t.Fatalf("the loaded report is wrong: %+v", loaded)
	}
	if string(loaded.Schema) != `{"type": "object"}` {
		t.Fatalf("the schema must ride verbatim: %s", loaded.Schema)
	}
	skipped := reports[1]
	if !skipped.Skipped || skipped.Reason != "NameError: name 'x' is not defined" {
		t.Fatalf("the skipped report is wrong: %+v", skipped)
	}
}

func TestDiscoverCheckedPreflightsBeforeExecution(t *testing.T) {
	k := &fakeKernel{}
	_, err := DiscoverChecked(context.Background(), k, []string{"/h/plugins/bash.py"}, map[string]bool{"bash": true}, PluginContract)
	if err == nil || !strings.Contains(err.Error(), "name collision") {
		t.Fatalf("a native collision must refuse before discovery: %v", err)
	}
	if len(k.cells) != 0 {
		t.Fatalf("the colliding file executed %d cells", len(k.cells))
	}

	k = &fakeKernel{}
	reports, err := DiscoverChecked(context.Background(), k, []string{"/h/plugins/Bad-Name.py"}, nil, PluginContract)
	if err != nil || len(reports) != 1 || !reports[0].Skipped || !strings.Contains(reports[0].Reason, "invalid plugin name") {
		t.Fatalf("the invalid filename must become a skipped report: %+v, %v", reports, err)
	}
	if len(k.cells) != 0 {
		t.Fatalf("the invalid filename executed %d cells", len(k.cells))
	}
}

func TestDiscoverKernelFailureIsTheError(t *testing.T) {
	t.Run("the kernel's reason rides the error", func(t *testing.T) {
		k := &fakeKernel{replies: []pythontool.Reply{errReply("kernel exited (code 1)", "")}}
		_, err := Discover(context.Background(), k, []string{"/h/plugins/echo.py"}, PluginContract)
		if err == nil || !strings.Contains(err.Error(), "kernel exited (code 1)") {
			t.Fatalf("the error must carry the kernel's reason, got %v", err)
		}
	})
	t.Run("a non-JSON report names the shape", func(t *testing.T) {
		k := &fakeKernel{replies: []pythontool.Reply{okReply("not a list")}}
		_, err := Discover(context.Background(), k, []string{"/h/plugins/echo.py"}, PluginContract)
		if err == nil || !strings.Contains(err.Error(), "not a JSON list") {
			t.Fatalf("the error must name the shape, got %v", err)
		}
	})
	t.Run("a transport error rides as-is", func(t *testing.T) {
		k := &fakeKernel{errs: []error{context.DeadlineExceeded}}
		_, err := Discover(context.Background(), k, []string{"/h/plugins/echo.py"}, PluginContract)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("the transport error must ride as-is, got %v", err)
		}
	})
}

func TestDiscoverCellCarriesTheFiles(t *testing.T) {
	k := &fakeKernel{replies: []pythontool.Reply{okReply("[]")}}
	_, err := Discover(context.Background(), k, []string{`/h/my dir/we'ird.py`, "/h/ok.py"}, PluginContract)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(k.cells) != 1 {
		t.Fatalf("cells = %d, want 1", len(k.cells))
	}
	cell := k.cells[0]
	for _, want := range []string{
		`_rig_j.loads('["/h/my dir/we\'ird.py","/h/ok.py"]')`,
		"importlib.util",
		"spec_from_file_location",
		`["DESCRIPTION","SCHEMA","run"]`,
		`[["DESCRIPTION","str"],["SCHEMA","dict"],["run","callable"]]`,
		`_f + " must be a " + _k`,
		"not isinstance(_v, str)",
		"not isinstance(_v, dict)",
		"not callable(_v)",
		"_rig_sys.modules[_rig_n] = _rig_m\n        _rig_s.loader.exec_module(_rig_m)",
		"_rig_sys.path.insert(0, _rig_d)",
		`for _rig_n in set(globals().get("__rig_plugins__", {})):`,
		"_rig_sys.modules.pop(_rig_n, None)",
		"__rig_plugins__",
		`_rig_j.dumps(_rig_report)`,
	} {
		if !strings.Contains(cell, want) {
			t.Fatalf("the discovery cell is missing %q:\n%s", want, cell)
		}
	}
}

func TestCallCellIsTotal(t *testing.T) {
	tt := &fakeKernel{}
	tool := New("echo", "echoes", "/h/plugins/echo.py", json.RawMessage(`{"type":"object"}`), tt)
	args, _ := json.Marshal(map[string]any{"text": "it's \"quoted\"\nand unicode: 世界"})
	if _, err := tool.Exec(context.Background(), args); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if len(tt.cells) != 1 {
		t.Fatalf("cells = %d, want 1", len(tt.cells))
	}
	cell := tt.cells[0]

	if strings.Contains(cell, "\n\"") {
		t.Fatalf("the args literal must be compact (no raw newlines):\n%s", cell)
	}
	if !strings.Contains(cell, `it\'s`) {
		t.Fatalf("the single quote must be backslashed in the embedding:\n%s", cell)
	}
	if !strings.Contains(cell, `\\"quoted\\"`) {
		t.Fatalf("the double quotes ride the JSON escaping (doubled by the embedding):\n%s", cell)
	}
	if !strings.Contains(cell, "世界") {
		t.Fatalf("unicode rides the literal verbatim:\n%s", cell)
	}
	if !strings.Contains(cell, `__rig_plugins__["echo"]`) {
		t.Fatalf("the call must name the stem's quoted form:\n%s", cell)
	}
}

func TestPyLiteralRefusesRawLineBreaks(t *testing.T) {
	for _, in := range []string{"x\ny", "x\ry", "x\r\ny"} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("pyLiteral(%q): want a panic, the raw break would break out of the literal", in)
				}
			}()
			pyLiteral(in)
		}()
	}
	raw, err := json.Marshal("x\ny\rz")
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if got := pyLiteral(string(raw)); strings.ContainsAny(got, "\n\r") {
		t.Errorf("the escaped literal must hold no raw break: %q", got)
	}
}

func TestToolExecRoundTripsArgsAndResult(t *testing.T) {
	k := &fakeKernel{replies: []pythontool.Reply{okReply("echo: hello rig\n")}}
	tool := New("echo", "echoes", "/h/plugins/echo.py", json.RawMessage(`{"type":"object"}`), k)
	out, err := tool.Exec(context.Background(), json.RawMessage(`{"text":"hello rig"}`))
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if out != "echo: hello rig" {
		t.Fatalf("the result = %q, want the printed value (the trailing newline dropped)", out)
	}
	t.Run("an empty value is an empty result", func(t *testing.T) {
		k2 := &fakeKernel{replies: []pythontool.Reply{okReply("\n")}}
		tool2 := New("echo", "echoes", "/h/plugins/echo.py", json.RawMessage(`{}`), k2)
		out2, err2 := tool2.Exec(context.Background(), json.RawMessage(`{}`))
		if err2 != nil || out2 != "" {
			t.Fatalf("empty value = (%q, %v), want (\"\", nil)", out2, err2)
		}
	})
}

func TestToolExecErrorCarriesTheTracebackTail(t *testing.T) {
	k := &fakeKernel{replies: []pythontool.Reply{errReply("ValueError: bad args", "partial output\n")}}
	tool := New("boom", "explodes", "/h/plugins/boom.py", json.RawMessage(`{}`), k)
	out, err := tool.Exec(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("a non-OK reply must be a tool error")
	}
	if err.Error() != "boom: ValueError: bad args" {
		t.Fatalf("the error = %q, want name: tail", err.Error())
	}
	if out != "partial output\nboom: ValueError: bad args" {
		t.Fatalf("the content = %q, want the partial output plus the tail", out)
	}
	t.Run("no partial output: the error is the content", func(t *testing.T) {
		k2 := &fakeKernel{replies: []pythontool.Reply{errReply("KeyError: 'x'", "")}}
		tool2 := New("boom", "explodes", "/h/plugins/boom.py", json.RawMessage(`{}`), k2)
		out2, err2 := tool2.Exec(context.Background(), json.RawMessage(`{}`))
		if out2 != "" || err2 == nil || err2.Error() != "boom: KeyError: 'x'" {
			t.Fatalf("(out, err) = (%q, %v); the loop feeds the error text back", out2, err2)
		}
	})
	t.Run("a malformed args dict is a refusal before the kernel", func(t *testing.T) {
		k3 := &fakeKernel{}
		tool3 := New("boom", "explodes", "/h/plugins/boom.py", json.RawMessage(`{}`), k3)
		_, err3 := tool3.Exec(context.Background(), json.RawMessage(`{not json`))
		if err3 == nil || !strings.Contains(err3.Error(), "not JSON") {
			t.Fatalf("a malformed args dict must refuse loud, got %v", err3)
		}
		if len(k3.cells) != 0 {
			t.Fatalf("the refusal is before the kernel (cells = %d)", len(k3.cells))
		}
	})
}

func TestToolSurfacesCarryTheFileContract(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`)
	tool := New("echo", "the fixture echo plugin", "/h/plugins/echo.py", schema, &fakeKernel{})
	if tool.Name() != "echo" {
		t.Fatalf("Name = %q, want the stem", tool.Name())
	}
	if tool.Description() != "the fixture echo plugin" {
		t.Fatalf("Description = %q, want the file's DESCRIPTION verbatim", tool.Description())
	}
	if string(tool.Schema()) != string(schema) {
		t.Fatalf("Schema = %s, want the file's SCHEMA verbatim", tool.Schema())
	}
	if tool.File() != "/h/plugins/echo.py" {
		t.Fatalf("File = %q, want the file's path", tool.File())
	}
}

func TestStaticDescriptionReadsEveryForm(t *testing.T) {
	cases := map[string]string{
		`DESCRIPTION = "plain"`:                                            "plain",
		`DESCRIPTION = 'single'`:                                           "single",
		"DESCRIPTION = \"\"\"triple\nline\"\"\"":                           "triple\nline",
		"DESCRIPTION = (\"one, \"\n                 \"two\")\nSCHEMA = {}": "one, two",
		"DESCRIPTION = (\n    'a '  # a note\n    'b'\n)":                  "a b",
		`DESCRIPTION = "esc \"q\" and \\n"`:                                "esc \"q\" and \\n",
		`DESCRIPTION = ("x" "y") + "z"`:                                    "xy",
		`DESCRIPTION = f"computed {x}"`:                                    "computed {x}",
		`NAME = "n"`:                                                       "",
		`DESCRIPTION = ("unterminated`:                                     "",
	}
	for src, want := range cases {
		if got := StaticDescription(src); got != want {
			t.Errorf("%q -> %q, want %q", src, got, want)
		}
	}
}

func TestTheDiscoveryCellRefusesAStdlibShadowBeforeItExecutes(t *testing.T) {
	cell := discoveryCell([]string{"/h/plugins/json.py"}, PluginContract)
	check := strings.Index(cell, "stdlib_module_names")
	if check < 0 {
		t.Fatalf("the cell must refuse a stdlib-shadowing stem:\n%s", cell)
	}
	if exec := strings.Index(cell, "exec_module"); exec >= 0 && check > exec {
		t.Fatalf("the shadow refusal must run before the module executes:\n%s", cell)
	}
}

func TestAJsonNamedPluginRefusesAndANormalOneLoads(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3 on PATH; the discovery cell needs one")
	}
	dir := t.TempDir()
	shadow := filepath.Join(dir, "json.py")
	normal := filepath.Join(dir, "echo.py")
	src := "DESCRIPTION = 'x'\nSCHEMA = {}\ndef run(args):\n    return 'x'\n"
	if err := os.WriteFile(shadow, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(normal, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(py, "-c", discoveryCell([]string{shadow, normal}, PluginContract)).Output()
	if err != nil {
		t.Fatalf("the cell must run: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	var reports []wireReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &reports); err != nil {
		t.Fatalf("the report = %q, want the JSON list: %v", string(out), err)
	}
	if len(reports) != 2 {
		t.Fatalf("two files, two reports: %+v", reports)
	}
	if reports[0].Name != "json" || reports[0].OK || !strings.Contains(reports[0].Error, "shadows a stdlib module") {
		t.Fatalf("the json-named plugin must refuse with the collision named: %+v", reports[0])
	}
	if reports[1].Name != "echo" || !reports[1].OK {
		t.Fatalf("a normal plugin must load beside it: %+v", reports[1])
	}
}

func TestAKernelReportThatOmitsAFileSkipsItLoud(t *testing.T) {
	report := `[{"name":"echo","file":"/h/plugins/echo.py","ok":true,"description":"echoes","schema":{"type":"object"}}]`
	k := &fakeKernel{replies: []pythontool.Reply{okReply(report)}}
	reports, err := DiscoverChecked(context.Background(), k, []string{"/h/plugins/echo.py", "/h/plugins/ghost.py"}, nil, PluginContract)
	if err != nil {
		t.Fatalf("DiscoverChecked: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("every file must surface, got %d: %+v", len(reports), reports)
	}
	if ghost := reports[1]; !ghost.Skipped || !strings.Contains(ghost.Reason, "omitted") {
		t.Fatalf("the omitted file must skip loud: %+v", ghost)
	}
}

func TestAPluginImportingASiblingLoads(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3 on PATH; the discovery cell needs one")
	}
	dir := t.TempDir()
	sibling := filepath.Join(dir, "z_sibling.py")
	alpha := filepath.Join(dir, "alpha.py")
	if err := os.WriteFile(sibling, []byte("TAG = 'sibling'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "import z_sibling\nDESCRIPTION = z_sibling.TAG\nSCHEMA = {}\ndef run(args):\n    return z_sibling.TAG\n"
	if err := os.WriteFile(alpha, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(py, "-c", discoveryCell([]string{alpha, sibling}, PluginContract)).Output()
	if err != nil {
		t.Fatalf("the cell must run: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	var reports []wireReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &reports); err != nil {
		t.Fatalf("the report = %q: %v", string(out), err)
	}
	byName := map[string]wireReport{}
	for _, rep := range reports {
		byName[rep.Name] = rep
	}
	if rep, ok := byName["alpha"]; !ok || !rep.OK {
		t.Fatalf("the importing plugin must load: %+v", byName["alpha"])
	}
	if rep, ok := byName["z_sibling"]; ok && strings.Contains(rep.Error, "ModuleNotFoundError") {
		t.Fatalf("the sibling must import cleanly: %+v", rep)
	}
}

func TestInvokeStreamsProgressAndSplitsTheResult(t *testing.T) {
	k := &streamingKernel{}
	var got []string
	raw, err := Invoke(context.Background(), k, "trainer", "fake", "train", 0,
		func(chunk string) { got = append(got, chunk) }, "/rows", "/out")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"checkpoint":"/x/ck"}` {
		t.Fatalf("the result is the cell's last line: %s", raw)
	}
	if len(got) != 2 || got[0] != "epoch 1\n" || got[1] != "epoch 2\n" {
		t.Fatalf("the cell's stdout streams to the caller: %q", got)
	}
}

type streamingKernel struct{}

func (k *streamingKernel) Run(ctx context.Context, code string, timeoutMs int, onStream func(string)) (pythontool.Reply, error) {
	for _, chunk := range []string{"epoch 1\n", "epoch 2\n"} {
		if onStream != nil {
			onStream(chunk)
		}
	}
	return okReply("epoch 1\nepoch 2\n" + `{"checkpoint":"/x/ck"}`), nil
}
