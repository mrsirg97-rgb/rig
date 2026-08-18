package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

// --- shared helpers ---

// goldenDir is the 0.2.0 wire fixtures: captured from the 0.2.0 build
// with no user files present (SPEC_CONFIG 9: the invariant's goldens).
const goldenDir = "testdata/golden_020"

type bodySrv struct {
	mu     sync.Mutex
	bodies [][]byte
	// reply is the canned SSE answer (default: pong).
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

// systemOf pulls the system message out of a captured request body.
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

// rigEnv is the scratch-home env: no user files unless the test writes
// them, and the binary's own PATH for the fake crontab when named.
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
	return filepath.Join(scratch, "rig")
}

// --- the named cases (SPEC_CONFIG, testing) ---

// TestNoUserFilesIsByteIdenticalToV020 (SPEC_CONFIG 9): with no user
// files present, every entry mode's request body is the 0.2.0 bytes —
// pinned against the fixtures captured from the 0.2.0 build. The named
// exception is the /models role column (a new surface), covered by the
// command tests.
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
		want, err := os.ReadFile(filepath.Join(goldenDir, "oneshot.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(s.last(), want) {
			t.Fatalf("the one-shot request body is not the 0.2.0 bytes:\ngot:\n%s\nwant:\n%s", s.last(), want)
		}
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
		want, err := os.ReadFile(filepath.Join(goldenDir, "repl.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(s.last(), want) {
			t.Fatalf("the REPL request body is not the 0.2.0 bytes:\ngot:\n%s\nwant:\n%s", s.last(), want)
		}
	})
	t.Run("runjob", func(t *testing.T) {
		s := &bodySrv{}
		srv := newBodySrv(t, s)
		binDir := t.TempDir()
		bin := buildBin(t, binDir)
		scratch := t.TempDir()
		workDir := t.TempDir()
		writeFakeCrontab(t, binDir, filepath.Join(scratch, "spool"))

		// the job: created in-process through the real verb with scripted
		// seams, exactly as the agent-side tool would create it.
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

		cmd := exec.Command(bin, "run-job", key)
		cmd.Dir = workDir
		cmd.Env = append(rigEnv(scratch, binDir), "RIG_SWAP_URL="+srv.URL)
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("run-job exited non-zero (recorded outcomes exit 0): %v\n%s", runErr, out)
		}
		// the worker's request: the busy check's two GETs plus the one
		// chat completion.
		if s.count() != 1 {
			t.Fatalf("worker requests = %d, want 1 (the worker's chat call)", s.count())
		}
		got := s.last()
		// the worker's prompt is the job prompt plus the standing report-
		// back directive (the argv's -p, pinned through the wire).
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
		// and the body is the 0.2.0 bytes, whole.
		want, err := os.ReadFile(filepath.Join(goldenDir, "runjob.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("the worker's request body is not the 0.2.0 bytes:\ngot:\n%s\nwant:\n%s", got, want)
		}
	})
}

// TestPrecedenceFlagOverEnvOverFileOverEmbedded (SPEC_CONFIG 2, named):
// one key (system), four runs — each layer wins when the layers above
// are absent: embedded alone, then file, then env, then flag.
func TestPrecedenceFlagOverEnvOverFileOverEmbedded(t *testing.T) {
	const embeddedSystem = "You are rig, a minimal coding agent. Use the provided tools to inspect, change, and run things in the working directory. Answer in plain text when done."
	cases := []struct {
		name string
		file string // "" = no settings.json
		env  string // "" = RIG_SYSTEM unset
		flag string // "" = -system absent
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

// TestFlagPresenceWins (SPEC_CONFIG 2, named): a passed flag wins,
// whatever its value — the flag-presence rule, the 0.2.0 semantics
// preserved. -system "" runs with the empty system prompt (not the
// embedded default); -retries 0 reaches the guard's floor (clamped to
// 1, not the embedded 3).
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
		// the scripted provider: an identical failing bash call, then the
		// model's re-issuance. The guard with limit 1 refuses the second
		// re-issuance naming the bound; the embedded 3 would have let it
		// execute (and named 3).
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
				// the re-issuance: the same failing call
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
		// the second tool result (the refused re-issuance) names the
		// bound: with retries 0 clamped to 1, it is "failed 1 times"; the
		// embedded 3 would name "failed 3 times".
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

// TestPrecedencePresenceKeyEnvEmptyBeatsFile (SPEC_CONFIG 2, 5, named):
// the file sets a proxy, the env sets it empty — the env's set-empty
// wins: the client dials the target directly and the proxy sees nothing.
// The sibling case (the env unset) proves the file's proxy is a live dial
// target, so the zero hits above are the env's doing, not a wiring
// absence. The target is a public TEST-NET address: the fetch tool's
// private-address guard accepts it; the direct dial then takes the tool's
// 30s budget (blackholed SYN), which the run reports as a tool error the
// turn completes around.
func TestPrecedencePresenceKeyEnvEmptyBeatsFile(t *testing.T) {
	// the proxy the file names: a loopback counter.
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

	// the scripted provider: one web_fetch of the target, then the final
	// answer carrying whatever the tool returned.
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

// TestRunJobSwapUrlChain (SPEC_CONFIG 5, named): the file's swapUrl
// reaches the runner's busy check; RIG_SWAP_URL beats it; neither takes
// the embedded. Each case is observed by which endpoint the busy check
// hit.
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
		// two live endpoints: the file's and the env's. Only the env's
		// must see the busy probe.
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
		// the embedded default, observed on its own port.
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

// TestRunJobWorkerInheritsJobCwdAgents (SPEC_CONFIG 6, named): the
// worker inherits its own cwd's AGENTS.md plus the global — not the
// creating session's project file.
func TestRunJobWorkerInheritsJobCwdAgents(t *testing.T) {
	const global = "GLOBAL-AGENTS"
	const jobAgents = "JOB-AGENTS"
	const sessAgents = "SESS-AGENTS"

	// the scripted worker endpoint records the system message.
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
	// the global AGENTS.md (the worker's and the run-job's config home
	// is the scratch home).
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
	cmd := exec.Command(bin, "run-job", key)
	cmd.Dir = sessDir // the creating session's cwd: its project file must NOT ride
	cmd.Env = append(rigEnv(scratch, binDir), "RIG_SWAP_URL="+srv.URL)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("run-job: %v\n%s", runErr, out)
	}
	sysMu.Lock()
	sys := workerSystem
	sysMu.Unlock()
	const defaultSystem = "You are rig, a minimal coding agent. Use the provided tools to inspect, change, and run things in the working directory. Answer in plain text when done."
	want := defaultSystem + "\n\n" + global + "\n\n" + jobAgents
	if sys != want {
		t.Fatalf("the worker's system message = %q, want the default plus the global and the job cwd's AGENTS.md (not the session's):\n%q", sys, want)
	}
}

// TestAgentsOrderAgainstGuidelines (SPEC_CONFIG 6, named): the
// assembly is system + AGENTS.md + guidelines, in that order, when a
// guideline participant is present.
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

// TestRowEnvBeatsFileForActiveID (SPEC_CONFIG 4, named): a file row for
// the active id plus RIG_MODEL_WINDOW — the in-effect row (and the
// table's listing of it) carries the env's window; the file row lists
// under its id.
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
	// the file row: the overlay over the embedded (window 32768).
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
	if listed != resolved {
		t.Fatalf("/models would list %#+v, want the in-effect row %#+v", listed, resolved)
	}
}

// TestDefaultJobModelFromSettings (SPEC_CONFIG 5, named): the file's
// defaultJobModel reaches the tool — the description names it, and a
// create without a model lands it in the job row.
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
	if !strings.Contains(tool.Description(), "Default model: brain") {
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

// TestMalformedConfigRefusesBeforeStores (SPEC_CONFIG 3, named): a
// malformed settings.json — exit 1, the voice, and no state store
// created. The refusal is before any store.
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

// TestModelsFileRowListsAndSwitches (SPEC_CONFIG 4, named): a
// file-added row — /models lists it with its role, models <id>
// switches, and the next turn's request carries its model.
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
	if h.r.row != brain {
		t.Fatalf("the active row = %+v, want the file's brain row", h.r.row)
	}
	h.finish(done)
}
