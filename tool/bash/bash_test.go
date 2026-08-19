package bash_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/tool/bash"
)

func argsJSON(t *testing.T, args map[string]any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestExecutesCommandAndReturnsOutput(t *testing.T) {
	tool := bash.New()
	if tool.Name() != "bash" {
		t.Fatalf("name = %q, want bash", tool.Name())
	}
	got, err := tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"command": "echo hello",
	}))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got != "hello\n" {
		t.Fatalf("output = %q, want %q", got, "hello\n")
	}
}

func TestNonZeroExitIsAFedBackError(t *testing.T) {
	tool := bash.New()
	_, err := tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"command": "echo visible; exit 3",
	}))
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("non-zero exit must be an error naming the status, got %v", err)
	}
}

// TestFailureNamesTheCwd (SPEC_UX 3, named): the failing reply gains one
// trailing line naming the working directory — the "where am I" probe
// after a path error, one line and only on failure.
func TestFailureNamesTheCwd(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tool := bash.New()
	// the default cwd: the process's working directory
	got, err := tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"command": "echo visible; exit 3",
	}))
	if err == nil {
		t.Fatal("the failure must stay an error")
	}
	if want := "visible\n(cwd " + wd + ")"; got != want {
		t.Fatalf("the failing reply must name the cwd as its trailing line, got %q", got)
	}
	// an explicit cwd: that directory, not the process's
	got, err = tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"command": "false",
		"cwd":     dir,
	}))
	if err == nil {
		t.Fatal("the failure must stay an error")
	}
	if want := "(cwd " + dir + ")"; got != want {
		t.Fatalf("an empty failure still names the cwd alone (no leading blank), got %q", got)
	}
}

// TestSuccessStaysByteIdentical (SPEC_UX 3, named): the cwd line is a
// failure's — the success is the piped golden's bytes, with or without
// an explicit cwd.
func TestSuccessStaysByteIdentical(t *testing.T) {
	tool := bash.New()
	got, err := tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"command": "echo hello",
	}))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got != "hello\n" {
		t.Fatalf("the success is byte-identical, got %q", got)
	}
	dir := t.TempDir()
	got, err = tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"command": "pwd",
		"cwd":     dir,
	}))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got != dir+"\n" {
		t.Fatalf("the explicit cwd's success is byte-identical, got %q", got)
	}
}

func TestStderrIsIncluded(t *testing.T) {
	tool := bash.New()
	got, err := tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"command": "echo noise >&2",
	}))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got != "noise\n" {
		t.Fatalf("stderr must be included in the surfaced output, got %q", got)
	}
}

func TestCwdIsRespected(t *testing.T) {
	dir := t.TempDir()
	tool := bash.New()
	got, err := tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"command": "pwd",
		"cwd":     dir,
	}))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got != dir+"\n" {
		t.Fatalf("pwd = %q, want %q", got, dir+"\n")
	}
}

func TestMissingCommandFailsLoud(t *testing.T) {
	tool := bash.New()
	_, err := tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"command": "/definitely/not/a/binary",
	}))
	if err == nil {
		t.Fatal("missing binary must fail loudly")
	}
}

func TestEmptyCommandFailsLoud(t *testing.T) {
	tool := bash.New()
	_, err := tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"command": "",
	}))
	if err == nil {
		t.Fatal("empty command must fail loudly")
	}
}

func TestUnknownArgFailsLoud(t *testing.T) {
	tool := bash.New()
	_, err := tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"command": "true",
		"bogus":   true,
	}))
	if err == nil {
		t.Fatal("unknown args must be refused, not silently ignored")
	}
}

func TestCancellationKillsTheProcess(t *testing.T) {
	tool := bash.New()
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	done := make(chan struct{})
	go func() {
		tool.Exec(ctx, argsJSON(t, map[string]any{"command": "sleep 30"}))
		close(done)
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled exec must not outlive the cancellation")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cancellation took %v, want prompt teardown", elapsed)
	}
}

func TestBackgroundChildDoesNotHoldTheTurn(t *testing.T) {
	tool := bash.New()
	start := time.Now()
	got, err := tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"command": "sleep 30 & echo started",
	}))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(got, "started") {
		t.Fatalf("output = %q, want the foreground output", got)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("background child held the turn for %v, want a bounded exit", elapsed)
	}
}

func TestOutputIsCapped(t *testing.T) {
	tool := bash.New()
	// ~1MiB of zeros; well over any sane bound
	got, err := tool.Exec(context.Background(), argsJSON(t, map[string]any{
		"command": "yes | head -c 1048576",
	}))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if len(got) >= 1048576 {
		t.Fatalf("unbounded output: %d bytes", len(got))
	}
	if !strings.HasSuffix(got, "[output truncated]") {
		t.Fatal("capped output must name the truncation for the model")
	}
}
