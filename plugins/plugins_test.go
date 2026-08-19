// Package plugins tests: the discovery and call semantics against a
// fake kernel (SPEC_PLUGINS, testing) — no python required; the e2e
// (real kernel) lives in cmd/rig.
package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	pythontool "github.com/mrsirg97-rgb/rig/tool/python"
)

// fakeKernel is the DI seam's stand-in: it records every cell and
// returns the canned replies in order.
type fakeKernel struct {
	cells   []string
	replies []pythontool.Reply
	errs    []error
}

func (f *fakeKernel) Run(ctx context.Context, code string, timeoutMs int) (pythontool.Reply, error) {
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

// TestDiscoverParsesTheKernelReport (SPEC_PLUGINS, named): a canned
// report (one loaded, one skipped) — the Reports carry the fields, in
// file order.
func TestDiscoverParsesTheKernelReport(t *testing.T) {
	report := `
[
  {"name": "echo", "file": "/h/plugins/echo.py", "ok": true, "description": "echoes", "schema": {"type": "object"}},
  {"name": "broken", "file": "/h/plugins/broken.py", "ok": false, "error": "NameError: name 'x' is not defined"}
]`
	k := &fakeKernel{replies: []pythontool.Reply{okReply(report + "\n")}}
	reports, err := Discover(context.Background(), k, []string{"/h/plugins/echo.py", "/h/plugins/broken.py"})
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

// TestDiscoverKernelFailureIsTheError (SPEC_PLUGINS, named): a non-OK
// reply — the error carries the kernel's reason; a report that is not
// a JSON list — the error names the shape.
func TestDiscoverKernelFailureIsTheError(t *testing.T) {
	t.Run("the kernel's reason rides the error", func(t *testing.T) {
		k := &fakeKernel{replies: []pythontool.Reply{errReply("kernel exited (code 1)", "")}}
		_, err := Discover(context.Background(), k, []string{"/h/plugins/echo.py"})
		if err == nil || !strings.Contains(err.Error(), "kernel exited (code 1)") {
			t.Fatalf("the error must carry the kernel's reason, got %v", err)
		}
	})
	t.Run("a non-JSON report names the shape", func(t *testing.T) {
		k := &fakeKernel{replies: []pythontool.Reply{okReply("not a list")}}
		_, err := Discover(context.Background(), k, []string{"/h/plugins/echo.py"})
		if err == nil || !strings.Contains(err.Error(), "not a JSON list") {
			t.Fatalf("the error must name the shape, got %v", err)
		}
	})
	t.Run("a transport error rides as-is", func(t *testing.T) {
		k := &fakeKernel{errs: []error{context.DeadlineExceeded}}
		_, err := Discover(context.Background(), k, []string{"/h/plugins/echo.py"})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("the transport error must ride as-is, got %v", err)
		}
	})
}

// TestDiscoverCellCarriesTheFiles (SPEC_PLUGINS, named): the cell the
// kernel would receive — the files embedded (quotes and spaces in a
// path survive), the three names validated, the registration named.
func TestDiscoverCellCarriesTheFiles(t *testing.T) {
	k := &fakeKernel{replies: []pythontool.Reply{okReply("[]")}}
	_, err := Discover(context.Background(), k, []string{`/h/my dir/we'ird.py`, "/h/ok.py"})
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
		`("DESCRIPTION", "SCHEMA", "run")`,
		"DESCRIPTION must be a str",
		"SCHEMA must be a dict",
		"run must be callable",
		"sys.modules.setdefault",
		"__rig_plugins__",
		`_rig_j.dumps(_rig_report)`,
	} {
		if !strings.Contains(cell, want) {
			t.Fatalf("the discovery cell is missing %q:\n%s", want, cell)
		}
	}
}

// TestCallCellIsTotal (SPEC_PLUGINS, named): the call cell for args
// with quotes, newlines, and unicode — the re-marshalled JSON is
// compact (no raw newlines) and the embedding parses.
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
	// the args literal: compact JSON (no raw newline), embedded in a
	// single-quoted python literal with the quote and backslash doubled.
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

// TestToolExecRoundTripsArgsAndResult (SPEC_PLUGINS, named): a canned
// OK reply (the printed value) — the result is the value, the trailing
// newline dropped; the error nil.
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

// TestToolExecErrorCarriesTheTracebackTail (SPEC_PLUGINS, named): a
// non-OK reply with the exception tail (and partial output) — the
// error is name: tail, the content carries the partial output plus
// the tail (the loop's content-over-error contract).
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

// TestToolSurfacesCarryTheFileContract (SPEC_PLUGINS, named): Name /
// Description / Schema are the report's — the wire's three, verbatim.
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
