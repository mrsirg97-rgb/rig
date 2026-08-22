package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/config"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/perm"
	"github.com/mrsirg97-rgb/rig/models"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	schedapi "github.com/mrsirg97-rgb/rig/tool/scheduler"
)

var goldenUpdateFlag = flag.Bool("update", false, "regenerate the golden_020 fixtures in place")

func goldenWrite(t *testing.T, name string, data []byte) {
	t.Helper()
	path := filepath.Join(goldenDir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("golden write: %v", err)
	}
}

func goldenCheck(t *testing.T, name string, data []byte) {
	t.Helper()
	if *goldenUpdateFlag {
		goldenWrite(t, name, data)
		return
	}
	want, err := os.ReadFile(filepath.Join(goldenDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, want) {
		t.Fatalf("the request body is not the pinned bytes:\ngot:\n%s\nwant:\n%s", data, want)
	}
}

const goldenDir = "testdata/golden_020"

type bodySrv struct {
	mu     sync.Mutex
	bodies [][]byte

	reply string
}

func newBodySrv(t *testing.T, s *bodySrv) *httptest.Server {
	t.Helper()
	if s.reply == "" {
		s.reply = `data: {"choices":[{"delta":{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == "GET" && (r.URL.Path == "/v1/models" || r.URL.Path == "/running"):
			if r.URL.Path == "/v1/models" {
				w.Write([]byte(`{"data":[]}`))
			} else {
				w.Write([]byte(`{"running":[]}`))
			}
			return
		default:
			s.mu.Lock()
			s.bodies = append(s.bodies, body)
			s.mu.Unlock()
			w.Write([]byte(s.reply))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *bodySrv) last() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		return nil
	}
	return s.bodies[len(s.bodies)-1]
}

func (s *bodySrv) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

func (s *bodySrv) bodiesAll() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.bodies...)
}

func netListen(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }

func systemOf(t *testing.T, body []byte) string {
	t.Helper()
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal the captured body: %v", err)
	}
	for _, m := range req.Messages {
		if m.Role == "system" {
			return m.Content
		}
	}
	return ""
}

func buildBin(t *testing.T, binDir string) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, "rig")
	if out, err := exec.Command("go", "build", "-o", bin, filepath.Join(root, "cmd", "rig")).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func rigEnv(scratch, binDir string) []string {
	env := append(os.Environ(),
		"HOME="+scratch,
		"XDG_CONFIG_HOME="+scratch,
	)
	if binDir != "" {
		env = append(env, "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return env
}

func cfgDir(t *testing.T, scratch string) string {
	t.Helper()
	return filepath.Join(scratch, ".rig")
}

func TestNoUserFilesIsByteIdenticalToV020(t *testing.T) {
	t.Run("oneshot", func(t *testing.T) {
		s := &bodySrv{}
		srv := newBodySrv(t, s)
		bin := buildBin(t, t.TempDir())
		scratch := t.TempDir()
		cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
		cmd.Dir = t.TempDir()
		cmd.Env = rigEnv(scratch, "")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the run must succeed: %v\n%s", err, out)
		}
		if s.count() != 1 {
			t.Fatalf("requests = %d, want 1", s.count())
		}
		goldenCheck(t, "oneshot.json", s.last())
	})
	t.Run("repl", func(t *testing.T) {
		s := &bodySrv{}
		srv := newBodySrv(t, s)
		bin := buildBin(t, t.TempDir())
		scratch := t.TempDir()
		cmd := exec.Command(bin, "-base-url", srv.URL+"/v1")
		cmd.Dir = t.TempDir()
		cmd.Env = rigEnv(scratch, "")
		cmd.Stdin = strings.NewReader("hello\n")
		out, err := cmd.CombinedOutput()
		_ = err
		if len(out) == 0 {
			t.Fatal("the run printed nothing")
		}
		if s.count() != 1 {
			t.Fatalf("requests = %d, want 1", s.count())
		}
		goldenCheck(t, "repl.json", s.last())
	})
	t.Run("runjob", func(t *testing.T) {
		s := &bodySrv{}
		srv := newBodySrv(t, s)
		binDir := t.TempDir()
		bin := buildBin(t, binDir)
		scratch := t.TempDir()
		workDir := t.TempDir()
		writeFakeCrontab(t, binDir, filepath.Join(scratch, "spool"))

		home := filepath.Join(cfgDir(t, scratch), "scheduler")
		fake := newFakeCrontab()
		st := scratchStores(t, home, "/ws/golden")
		reply, err := sched.Create(context.Background(), st, fake, sched.CreateInput{
			Name: "golden", Prompt: "say hi", Cron: "0 5 * * *", Scope: "cwd",
			Cwd: workDir, Model: "qwen3.8-workers", Busy: "skip",
		}, "/ws/golden", "sess-golden", bin+" run-job", fixedNow)
		if err != nil {
			t.Fatalf("create: %v (%s)", err, reply)
		}
		key := "cwd-" + sched.CwdHash("/ws/golden") + ":j1"
		if err := os.WriteFile(filepath.Join(scratch, "spool"),
			[]byte("0 5 * * * "+bin+" run-job # pane-scheduler:"+key+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		sandboxOff(t, scratch)

		cmd := exec.Command(bin, "run-job", key)
		cmd.Dir = workDir
		cmd.Env = append(rigEnv(scratch, binDir), "RIG_SWAP_URL="+srv.URL)
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("run-job exited non-zero (recorded outcomes exit 0): %v\n%s", runErr, out)
		}

		if s.count() != 1 {
			t.Fatalf("worker requests = %d, want 1 (the worker's chat call)", s.count())
		}
		got := s.last()

		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(got, &req); err != nil {
			t.Fatal(err)
		}
		foundUser := false
		for _, m := range req.Messages {
			if m.Role == "user" {
				foundUser = true
				if m.Content != "say hi"+sched.ReportBack {
					t.Fatalf("the worker's prompt = %q, want the job prompt plus the report-back directive", m.Content)
				}
			}
		}
		if !foundUser {
			t.Fatal("the worker request carries no user message")
		}
		if req.Model != "qwen3.8-workers" {
			t.Fatalf("the worker's model = %q, want the job's model (the argv's -model)", req.Model)
		}

		goldenCheck(t, "runjob.json", got)
	})
}

func TestPrecedenceFlagOverEnvOverFileOverEmbedded(t *testing.T) {
	const embeddedSystem = "You are rig, a minimal coding agent. Use the tools to inspect, change, and run things in the working directory; answer in plain text when done. The harness enforces its walls — an allowlist, a retry guard, an approval gate, a plugin landing zone — and names each refusal; a refusal is final for that call: change the call or ask, never reach the same effect through another tool. Memory is a tool: recall before re-deriving a project fact, learn deliberately what the next session should not re-derive, supersede by id when the code disagrees. Python is a persistent kernel: compute there, don't estimate; a capability you build twice belongs in a plugin."
	cases := []struct {
		name string
		file string
		env  string
		flag string
		want string
	}{
		{"embedded", "", "", "", embeddedSystem},
		{"file over embedded", `{"system": "FROMFILE"}`, "", "", "FROMFILE"},
		{"env over file", `{"system": "FROMFILE"}`, "FROMENV", "", "FROMENV"},
		{"flag over env over file", `{"system": "FROMFILE"}`, "FROMENV", "FROMFLAG", "FROMFLAG"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &bodySrv{}
			srv := newBodySrv(t, s)
			bin := buildBin(t, t.TempDir())
			scratch := t.TempDir()
			if c.file != "" {
				dir := cfgDir(t, scratch)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(c.file), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"-p", "hello", "-base-url", srv.URL + "/v1"}
			if c.flag != "" {
				args = append(args, "-system", c.flag)
			}
			cmd := exec.Command(bin, args...)
			cmd.Dir = t.TempDir()
			env := rigEnv(scratch, "")
			if c.env != "" {
				env = append(env, "RIG_SYSTEM="+c.env)
			}
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the run must succeed: %v\n%s", err, out)
			}
			if got := systemOf(t, s.last()); got != c.want {
				t.Fatalf("the system message = %q, want %q (%s wins)", got, c.want, c.name)
			}
		})
	}
}

func TestFlagPresenceWins(t *testing.T) {
	t.Run("an empty system flag is the choice", func(t *testing.T) {
		s := &bodySrv{}
		srv := newBodySrv(t, s)
		bin := buildBin(t, t.TempDir())
		cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1", "-system", "")
		cmd.Dir = t.TempDir()
		cmd.Env = rigEnv(t.TempDir(), "")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the run must succeed: %v\n%s", err, out)
		}
		if got := systemOf(t, s.last()); got != "" {
			t.Fatalf("the system message = %q, want empty (-system \"\" wins over the embedded default)", got)
		}
	})
	t.Run("retries zero clamps to the guard's floor", func(t *testing.T) {

		s := &bodySrv{}
		s.reply = `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			s.bodies = append(s.bodies, body)
			s.mu.Unlock()
			var req struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.Unmarshal(body, &req)
			toolResult := ""
			for _, m := range req.Messages {
				if m.Role == "tool" {
					toolResult = m.Content
				}
			}
			switch {
			case toolResult == "":
				w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"id":"c1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"false\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n"))
			case strings.Contains(toolResult, "bound exhausted"):
				w.Write([]byte(`data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n"))
			default:

				w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"id":"c2","type":"function","function":{"name":"bash","arguments":"{\"command\":\"false\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n"))
			}
		}))
		t.Cleanup(srv.Close)
		bin := buildBin(t, t.TempDir())
		cmd := exec.Command(bin, "-p", "run the probe", "-base-url", srv.URL+"/v1", "-retries", "0")
		cmd.Dir = t.TempDir()
		cmd.Env = rigEnv(t.TempDir(), "")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the run must succeed: %v\n%s", err, out)
		}

		boundLine := ""
		for _, body := range s.bodiesAll() {
			var req struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.Unmarshal(body, &req)
			for _, m := range req.Messages {
				if m.Role == "tool" && strings.Contains(m.Content, "bound exhausted") {
					boundLine = m.Content
				}
			}
		}
		if boundLine == "" {
			t.Fatalf("no refused re-issuance in the transcript: the retries floor was not in effect (bodies: %d)", len(s.bodiesAll()))
		}
		if !strings.Contains(boundLine, "failed 1 times") {
			t.Fatalf("the bound = %q, want the clamped 1 (not the embedded 3)", boundLine)
		}
	})
}

func TestPrecedencePresenceKeyEnvEmptyBeatsFile(t *testing.T) {

	hits := 0
	var mu sync.Mutex
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Write([]byte("PROXY-SERVED"))
	}))
	t.Cleanup(proxySrv.Close)
	const target = "http://203.0.113.10/page"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		toolResult := ""
		for _, m := range req.Messages {
			if m.Role == "tool" {
				toolResult = m.Content
			}
		}
		switch {
		case toolResult == "":
			w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"id":"c1","type":"function","function":{"name":"web_fetch","arguments":"` +
				`{\"url\":\"` + target + `\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n"))
		default:
			ans := strings.ReplaceAll(toolResult[:min(24, len(toolResult))], `"`, `\"`)
			w.Write([]byte(`data: {"choices":[{"delta":{"content":"RESULT: ` + ans + `"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n"))
		}
	}))
	t.Cleanup(srv.Close)

	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	dir := cfgDir(t, scratch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"webFetchProxy": "`+proxySrv.URL+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(envExtra ...string) string {
		cmd := exec.Command(bin, "-p", "fetch the page", "-base-url", srv.URL+"/v1")
		cmd.Dir = t.TempDir()
		cmd.Env = append(rigEnv(scratch, ""), envExtra...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the run must succeed: %v\n%s", err, out)
		}
		return string(out)
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return hits
	}

	t.Run("the empty env wins: direct dial, the proxy sees nothing", func(t *testing.T) {
		before := count()
		out := run(`RIG_WEB_FETCH_PROXY=`)
		if got := count() - before; got != 0 {
			t.Fatalf("the file's proxy received %d request(s); the empty env is set-empty, so the dial must go direct: %s", got, out)
		}
	})
	t.Run("the env unset: the file's proxy serves the fetch", func(t *testing.T) {
		before := count()
		out := run()
		if got := count() - before; got != 1 {
			t.Fatalf("the file's proxy received %d request(s), want 1 (the file's proxy is a live dial target): %s", got, out)
		}
		if !strings.Contains(out, "PROXY-SERVED") {
			t.Fatalf("the answer must carry the proxy-served body:\n%s", out)
		}
	})
}

func TestRunJobSwapUrlChain(t *testing.T) {
	mkJob := func(t *testing.T, scratch, bin string) (string, string) {
		t.Helper()
		workDir := t.TempDir()
		home := filepath.Join(cfgDir(t, scratch), "scheduler")
		fake := newFakeCrontab()
		st := scratchStores(t, home, "/ws/swap")
		reply, err := sched.Create(context.Background(), st, fake, sched.CreateInput{
			Name: "swap", Prompt: "say hi", Cron: "0 5 * * *", Scope: "cwd",
			Cwd: workDir, Model: "qwen3.8-workers", Busy: "skip",
		}, "/ws/swap", "sess-swap", bin+" run-job", fixedNow)
		if err != nil {
			t.Fatalf("create: %v (%s)", err, reply)
		}
		return "cwd-" + sched.CwdHash("/ws/swap") + ":j1", workDir
	}
	fire := func(t *testing.T, binDir, bin, scratch, key, workDir, swapURL string) {
		t.Helper()
		sp := filepath.Join(scratch, "spool")
		if err := os.WriteFile(sp,
			[]byte("0 5 * * * "+bin+" run-job # pane-scheduler:"+key+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bin, "run-job", key)
		cmd.Dir = workDir
		env := rigEnv(scratch, binDir)
		if swapURL != "" {
			env = append(env, "RIG_SWAP_URL="+swapURL)
		}
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("run-job: %v\n%s", err, out)
		}
	}
	t.Run("the file's swapUrl reaches the busy check", func(t *testing.T) {
		hit := make(chan bool, 1)
		fileURL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/models":
				select {
				case hit <- true:
				default:
				}
				w.Write([]byte(`{"data":[]}`))
			case "/running":
				w.Write([]byte(`{"running":[]}`))
			default:
				w.Write([]byte(`data: {"choices":[{"delta":{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n"))
			}
		}))
		t.Cleanup(fileURL.Close)
		binDir := t.TempDir()
		bin := buildBin(t, binDir)
		scratch := t.TempDir()
		writeFakeCrontab(t, binDir, filepath.Join(scratch, "spool"))
		dir := cfgDir(t, scratch)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "settings.json"),
			[]byte(`{"swapUrl": "`+fileURL.URL+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		key, workDir := mkJob(t, scratch, bin)
		fire(t, binDir, bin, scratch, key, workDir, "")
		select {
		case <-hit:
		default:
			t.Fatal("the file's swapUrl did not receive the busy check")
		}
	})
	t.Run("the env beats the file", func(t *testing.T) {

		envHit, fileHit := make(chan bool, 1), make(chan bool, 1)
		mk := func(ch chan bool) *httptest.Server {
			return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/models":
					select {
					case ch <- true:
					default:
					}
					w.Write([]byte(`{"data":[]}`))
				case "/running":
					w.Write([]byte(`{"running":[]}`))
				default:
					w.Write([]byte(`data: {"choices":[{"delta":{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n"))
				}
			}))
		}
		envURL := mk(envHit)
		t.Cleanup(envURL.Close)
		fileURL := mk(fileHit)
		t.Cleanup(fileURL.Close)

		binDir := t.TempDir()
		bin := buildBin(t, binDir)
		scratch := t.TempDir()
		writeFakeCrontab(t, binDir, filepath.Join(scratch, "spool"))
		dir := cfgDir(t, scratch)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "settings.json"),
			[]byte(`{"swapUrl": "`+fileURL.URL+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		key, workDir := mkJob(t, scratch, bin)
		fire(t, binDir, bin, scratch, key, workDir, envURL.URL)
		select {
		case <-envHit:
		default:
			t.Fatal("the env's swapUrl did not receive the busy check")
		}
		select {
		case <-fileHit:
			t.Fatal("the file's swapUrl received the busy check; the env must beat it")
		default:
		}
	})
	t.Run("neither takes the embedded", func(t *testing.T) {

		l, err := netListen("127.0.0.1:8090")
		if err != nil {
			t.Skipf("the embedded default's port is busy: %v", err)
		}
		defer l.Close()
		hit := make(chan bool, 1)
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/models" {
				select {
				case hit <- true:
				default:
				}
			}
			if r.URL.Path == "/v1/models" {
				w.Write([]byte(`{"data":[]}`))
			} else if r.URL.Path == "/running" {
				w.Write([]byte(`{"running":[]}`))
			} else {
				w.Write([]byte(`data: {"choices":[{"delta":{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n"))
			}
		}))
		srv.Listener = l
		srv.Start()
		t.Cleanup(srv.Close)

		binDir := t.TempDir()
		bin := buildBin(t, binDir)
		scratch := t.TempDir()
		writeFakeCrontab(t, binDir, filepath.Join(scratch, "spool"))
		key, workDir := mkJob(t, scratch, bin)
		fire(t, binDir, bin, scratch, key, workDir, "")
		select {
		case <-hit:
		default:
			t.Fatal("the embedded swapUrl did not receive the busy check")
		}
	})
}

func TestRunJobWorkerInheritsJobCwdAgents(t *testing.T) {
	const global = "GLOBAL-AGENTS"
	const jobAgents = "JOB-AGENTS"
	const sessAgents = "SESS-AGENTS"

	var sysMu sync.Mutex
	var workerSystem string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if r.URL.Path == "/v1/models" {
				w.Write([]byte(`{"data":[]}`))
			} else {
				w.Write([]byte(`{"running":[]}`))
			}
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		sysMu.Lock()
		for _, m := range req.Messages {
			if m.Role == "system" {
				workerSystem = m.Content
			}
		}
		sysMu.Unlock()
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n"))
	}))
	t.Cleanup(srv.Close)

	binDir := t.TempDir()
	bin := buildBin(t, binDir)
	scratch := t.TempDir()
	writeFakeCrontab(t, binDir, filepath.Join(scratch, "spool"))

	dir := cfgDir(t, scratch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "AGENTS.md"), []byte(jobAgents), 0o644); err != nil {
		t.Fatal(err)
	}
	sessDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sessDir, "AGENTS.md"), []byte(sessAgents), 0o644); err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(dir, "scheduler")
	fake := newFakeCrontab()
	st := scratchStores(t, home, workDir)
	reply, err := sched.Create(context.Background(), st, fake, sched.CreateInput{
		Name: "agents", Prompt: "say hi", Cron: "0 5 * * *", Scope: "cwd",
		Cwd: workDir, Model: "qwen3.8-workers", Busy: "skip",
	}, workDir, "sess-agents", bin+" run-job", fixedNow)
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	key := "cwd-" + sched.CwdHash(workDir) + ":j1"
	if err := os.WriteFile(filepath.Join(scratch, "spool"),
		[]byte("0 5 * * * "+bin+" run-job # pane-scheduler:"+key+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sandboxOff(t, scratch)
	cmd := exec.Command(bin, "run-job", key)
	cmd.Dir = sessDir
	cmd.Env = append(rigEnv(scratch, binDir), "RIG_SWAP_URL="+srv.URL)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("run-job: %v\n%s", runErr, out)
	}
	sysMu.Lock()
	sys := workerSystem
	sysMu.Unlock()
	const defaultSystem = "You are rig, a minimal coding agent. Use the tools to inspect, change, and run things in the working directory; answer in plain text when done. The harness enforces its walls — an allowlist, a retry guard, an approval gate, a plugin landing zone — and names each refusal; a refusal is final for that call: change the call or ask, never reach the same effect through another tool. Memory is a tool: recall before re-deriving a project fact, learn deliberately what the next session should not re-derive, supersede by id when the code disagrees. Python is a persistent kernel: compute there, don't estimate; a capability you build twice belongs in a plugin."
	want := defaultSystem + "\n\n" + global + "\n\n" + jobAgents
	if sys != want {
		t.Fatalf("the worker's system message = %q, want the default plus the global and the job cwd's AGENTS.md (not the session's):\n%q", sys, want)
	}
}

func TestAgentsOrderAgainstGuidelines(t *testing.T) {
	gw := guidelineMW{
		ToolMiddlewareFunc: func(next core.ToolExec) core.ToolExec { return next },
		text:               "GUIDELINE-PROSE",
	}
	r := testRoot(nullFrontend{})
	r.agents = "G\n\nP"
	r.middleware = []core.ToolMiddleware{perm.Allowlist("bash"), gw}
	wire(r)
	want := "be terse" + "\n\n" + "G\n\nP" + "\n\n" + "GUIDELINE-PROSE"
	if r.fullSystem != want {
		t.Fatalf("fullSystem = %q, want %q (system, then AGENTS.md, then the guidelines)", r.fullSystem, want)
	}
}

func TestRowEnvBeatsFileForActiveID(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"),
		[]byte(`[{"id": "local", "window": 32768}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir, t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	fileRow, ok := cfg.Models.Get("local")
	if !ok {
		t.Fatal("the merged table lost local")
	}
	if fileRow.Window != 32768 {
		t.Fatalf("the file row's window = %d, want the file's 32768", fileRow.Window)
	}

	env := map[string]string{"RIG_MODEL_WINDOW": "40000"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	resolved, err := models.Resolve(cfg.Models, "local", lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Window != 40000 {
		t.Fatalf("the in-effect row's window = %d, want the env's 40000 (env still wins for the active id)", resolved.Window)
	}
	if resolved.MaxTokens != 8192 || resolved.Reserve != 8192 || resolved.KeepRecent != 16384 {
		t.Fatalf("the in-effect row's other fields = %+v, want the row's (the env set only the window)", resolved)
	}

	runtime := runtimeTable(cfg.Models, "local", resolved)
	listed, ok := runtime.Get("local")
	if !ok {
		t.Fatal("the runtime table lost local (the file row must list under its id)")
	}
	if !reflect.DeepEqual(listed, resolved) {
		t.Fatalf("/models would list %#+v, want the in-effect row %#+v", listed, resolved)
	}
}

func TestDefaultJobModelFromSettings(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"defaultJobModel": "brain"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir, t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings.DefaultJobModel != "brain" {
		t.Fatalf("defaultJobModel = %q, want the file's brain", cfg.Settings.DefaultJobModel)
	}

	home := t.TempDir()
	st := scratchStores(t, home, "/ws/default")
	ct := newFakeCrontab()
	tool := schedapi.New(st, ct, "rig run-job", cfg.Settings.DefaultJobModel)
	if !strings.Contains(tool.Description(), "(default: brain)") {
		t.Fatalf("the tool description must name the file's default: %q", tool.Description())
	}
	raw, err := json.Marshal(map[string]any{
		"action": "create", "name": "defaulted", "prompt": "p",
		"cron": "0 5 * * *", "scope": "cwd",
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := tool.Exec(context.Background(), raw)
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	var m string
	if err := st.Cwd.DB.QueryRow(`SELECT model FROM jobs WHERE id = 'j1'`).Scan(&m); err != nil {
		t.Fatal(err)
	}
	if m != "brain" {
		t.Fatalf("the job's model = %q, want the file's default brain", m)
	}
}

func TestMalformedConfigRefusesBeforeStores(t *testing.T) {
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	dir := cfgDir(t, scratch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(`{"retries": "three"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-p", "hello")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(scratch, "")
	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("a malformed settings.json must refuse: %q", out)
	}
	want := "rig: config: " + p + `: retries: expected an integer, got "three"`
	if !strings.Contains(string(out), want) {
		t.Fatalf("the voice = %q, want %q", out, want)
	}
	glob, _ := filepath.Glob(filepath.Join(dir, "sessions", "*.sqlite"))
	if len(glob) != 0 {
		t.Fatalf("the state store was created despite the refusal: %v", glob)
	}
}

func TestRoundsAndResultCapEnvRefuseLoud(t *testing.T) {
	for _, tc := range []struct {
		env   string
		voice string
	}{
		{"RIG_ROUNDS", "rig: RIG_ROUNDS: expected an integer, got \"many\""},
		{"RIG_RESULT_CAP", "rig: RIG_RESULT_CAP: expected an integer, got \"big\""},
	} {
		t.Run(tc.env, func(t *testing.T) {
			bin := buildBin(t, t.TempDir())
			cmd := exec.Command(bin, "-p", "hello")
			cmd.Dir = t.TempDir()
			env := rigEnv(t.TempDir(), "")
			if tc.env == "RIG_RESULT_CAP" {
				env = append(env, tc.env+"=big")
			} else {
				env = append(env, tc.env+"=many")
			}
			cmd.Env = env
			out, runErr := cmd.CombinedOutput()
			if runErr == nil {
				t.Fatalf("an invalid %s must refuse: %q", tc.env, out)
			}
			if !strings.Contains(string(out), tc.voice) {
				t.Fatalf("the voice = %q, want %q", out, tc.voice)
			}
		})
	}
}

func TestModelsFileRowListsAndSwitches(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"),
		[]byte(`[{"id": "brain", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768, "role": "worker"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir, t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	brain, ok := cfg.Models.Get("brain")
	if !ok {
		t.Fatal("the merged table has no brain row")
	}
	local, ok := cfg.Models.Get("local")
	if !ok {
		t.Fatal("the merged table lost local")
	}
	h := newHarness(t, local, "local", cfg.Models)
	done := h.startRun()
	h.in <- "/models\n"
	h.waitOut("brain")
	listing := h.out.String()
	brainLine := ""
	for _, l := range strings.Split(listing, "\n") {
		if strings.Contains(l, "brain") {
			brainLine = l
		}
	}
	if brainLine == "" || !strings.Contains(brainLine, "worker") {
		t.Fatalf("the /models listing must carry brain's role (worker):\n%s", listing)
	}
	h.in <- "/models brain\n"
	h.waitOut("models: active is now brain")
	h.in <- "go\n"
	h.waitCount("pong", 1)
	modelsOut, _ := h.s.mainCalls()
	if len(modelsOut) == 0 || modelsOut[len(modelsOut)-1] != "brain" {
		t.Fatalf("the turn after the switch must carry brain, got %v", modelsOut)
	}
	if !reflect.DeepEqual(h.r.row, brain) {
		t.Fatalf("the active row = %+v, want the file's brain row", h.r.row)
	}
	h.finish(done)
}

func TestEmbeddedAllowIsTheNativeSet(t *testing.T) {
	cfg, err := config.Load(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, n := range cfg.Settings.Allow {
		allowed[n] = true
	}
	for _, n := range nativeToolNames {
		if !allowed[n] {
			t.Errorf("native %q is not in the embedded allow default", n)
		}
	}
	natives := map[string]bool{}
	for _, n := range nativeToolNames {
		natives[n] = true
	}
	for _, n := range cfg.Settings.Allow {
		if !natives[n] {
			t.Errorf("embedded allow names %q, which is not a native", n)
		}
	}
}

func TestToolMenuBudgetAndVocabulary(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(goldenDir, "oneshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Tools []struct {
			Function struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	const budget = 14000
	total := 0
	for _, tl := range wire.Tools {
		f := tl.Function
		total += len(f.Description) + len(f.Parameters)
		text := f.Description + string(f.Parameters)
		for _, bad := range []string{"pi ", "pane"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s carries another harness's voice: %q", f.Name, bad)
			}
		}
		if !strings.Contains(f.Description, "Guidelines:") {
			t.Errorf("%s has no Guidelines sentence", f.Name)
		}
		if !strings.Contains(f.Description, "Reply:") {
			t.Errorf("%s does not name its reply's shape", f.Name)
		}
	}
	if total > budget {
		t.Fatalf("the tool menu is %d chars on the wire, over the %d budget: trimming is a decision, name it", total, budget)
	}
	t.Logf("tool menu: %d chars of %d", total, budget)
}
