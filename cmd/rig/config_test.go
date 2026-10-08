package main

import (
	"context"
	"encoding/json"
	"errors"
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
	"sync/atomic"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/config"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/middleware/perm"
	"github.com/mrsirg97-rgb/rig/v2/models"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v2/testenv"
	schedapi "github.com/mrsirg97-rgb/rig/v2/tool/scheduler"
)

type bodySrv struct {
	mu     sync.Mutex
	bodies [][]byte

	reply string
	slots int
	model string
}

func newBodySrv(t *testing.T, s *bodySrv) *httptest.Server {
	t.Helper()
	if s.reply == "" {
		s.reply = `data: {"choices":[{"delta":{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/models":
			w.Write([]byte(`{"data":[{"id":"` + s.model + `","status":{"value":"loaded"}}]}`))
			return
		case r.Method == "GET" && r.URL.Path == "/running":
			w.Write([]byte(`{"running":[{"model":"` + s.model + `"}]}`))
			return
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/upstream/"):
			s.mu.Lock()
			slots := s.slots
			s.mu.Unlock()
			if slots == 0 {
				slots = 1
			}
			type slot struct {
				ID           int  `json:"id"`
				IsProcessing bool `json:"is_processing"`
			}
			var out []slot
			for i := 0; i < slots; i++ {
				out = append(out, slot{ID: i})
			}
			b, _ := json.Marshal(out)
			w.Write(b)
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
	return buildBinAt(t, binDir, "../..")
}

const localModelRow = `{"id": "local", "window": 65536, "maxTokens": 8192, "reserve": 8192, "keepRecent": 16384, "role": "interactive", "efforts": ["low", "medium", "xhigh"]}`

func writeModelRows(t *testing.T, dir string, rows ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "models.json")
	if err := os.WriteFile(p, []byte("["+strings.Join(rows, ", ")+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rigEnv(t *testing.T, scratch, binDir string, extra ...string) []string {
	t.Helper()
	dir := cfgDir(t, scratch)
	if _, err := os.Stat(filepath.Join(dir, "models.json")); os.IsNotExist(err) {
		writeModelRows(t, dir, localModelRow)
	}
	env := scrubSwap(os.Environ())
	env = append(env,
		"HOME="+scratch,
		"XDG_CONFIG_HOME="+scratch,
		"RIG_MODEL=local",
	)
	env = append(env, swapPinned(extra)...)
	if binDir != "" {
		env = append(env, "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return env
}

func scrubSwap(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "RIG_SWAP_URL=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func swapPinned(extra []string) []string {
	for _, kv := range extra {
		if strings.HasPrefix(kv, "RIG_SWAP_URL=") {
			return extra
		}
	}
	return append([]string{"RIG_SWAP_URL=" + testenv.ClosedSwapURL}, extra...)
}

func cfgDir(t *testing.T, scratch string) string {
	t.Helper()
	return filepath.Join(scratch, ".rig")
}

func TestPrecedenceFlagOverEnvOverFileOverEmbedded(t *testing.T) {
	const embeddedSystem = "you are an agent operating in rig, a minimal, general purpose harness, designed to help you get more done with less friction. you act on the session's workspace, using the available tools to inspect, change, and run things in it. the toolset is focused on purpose, with each tool's description saying when to use it. do not attempt to use a tool that does not exist in rig. the harness has guards: an allowlist, a retry guard (three identical failing calls to one tool in a turn exhaust the bound; a corrected call always executes), an approval gate, a plugin landing zone. every refusal names its rule and is there to guide you, not punish you. a refusal is final for that call: change the call or ask, never reach the same effect through another tool. when a tool fails, read the error and work out why before calling again. do not retry blindly, and stop when the environment or the plan is wrong. a capability you build twice belongs in a plugin. for any job of three or more steps, or one that touches several files, plan it in todo before the first edit: create the tasks, start one before working on it, complete or fail it when done, and leave the queue empty at the end. when the work is done, answer in plain text: what changed, what you verified, what is left."
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
			cmdDir := t.TempDir()
			cmd.Dir = cmdDir
			env := rigEnv(t, scratch, "")
			if c.env != "" {
				env = append(env, "RIG_SYSTEM="+c.env)
			}
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the run must succeed: %v\n%s", err, out)
			}
			want := c.want + "\n\n" + sessionSection(cmdDir, scratch)
			if got := systemOf(t, s.last()); got != want {
				t.Fatalf("the system message = %q, want %q (%s wins)", got, want, c.name)
			}
		})
	}
}

func effortOf(t *testing.T, body []byte) string {
	t.Helper()
	var req struct {
		Effort string `json:"reasoning_effort"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal the captured body: %v", err)
	}
	return req.Effort
}

func TestEffortPrecedenceFlagOverEnvOverRow(t *testing.T) {
	const rowWithEffort = `{"id": "local", "window": 65536, "maxTokens": 8192, "reserve": 8192, "keepRecent": 16384, "role": "interactive", "effort": "xhigh", "efforts": ["low", "medium", "xhigh"]}`
	cases := []struct {
		name string
		row  string
		env  string
		flag string
		want string
	}{
		{"the row's effort is the live default", rowWithEffort, "", "", "xhigh"},
		{"nothing names a level, the server default rides", "", "", "", ""},
		{"the env beats the row", rowWithEffort, "low", "", "low"},
		{"the flag beats the env and the row", rowWithEffort, "low", "medium", "medium"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &bodySrv{}
			srv := newBodySrv(t, s)
			bin := buildBin(t, t.TempDir())
			scratch := t.TempDir()
			if c.row != "" {
				writeModelRows(t, cfgDir(t, scratch), c.row)
			}
			args := []string{"-p", "hello", "-base-url", srv.URL + "/v1"}
			if c.flag != "" {
				args = append(args, "-effort", c.flag)
			}
			cmd := exec.Command(bin, args...)
			cmd.Dir = t.TempDir()
			env := rigEnv(t, scratch, "")
			if c.env != "" {
				env = append(env, "RIG_EFFORT="+c.env)
			}
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the run must succeed: %v\n%s", err, out)
			}
			if got := effortOf(t, s.last()); got != c.want {
				t.Fatalf("the request's effort = %q, want %q (%s wins)", got, c.want, c.name)
			}
		})
	}
}

func TestEffortFlagRefusesAnUnknownLevelByName(t *testing.T) {
	bin := buildBin(t, t.TempDir())
	t.Run("a level outside the row's efforts", func(t *testing.T) {
		home := t.TempDir()
		cmd := exec.Command(bin, "-p", "hello", "-effort", "bogus")
		cmd.Dir = t.TempDir()
		cmd.Env = rigEnv(t, home, "")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("an unknown level must refuse: %q", out)
		}
		for _, want := range []string{`"bogus"`, "not a level for local", "available: low, medium, xhigh"} {
			if !strings.Contains(string(out), want) {
				t.Fatalf("the refusal must name the level and the row's choices, missing %q: %q", want, out)
			}
		}
	})
	t.Run("a row without efforts", func(t *testing.T) {
		home := t.TempDir()
		writeModelRows(t, cfgDir(t, home), `{"id": "local", "window": 65536, "maxTokens": 8192, "reserve": 8192, "keepRecent": 16384, "role": "interactive"}`)
		cmd := exec.Command(bin, "-p", "hello", "-effort", "low")
		cmd.Dir = t.TempDir()
		cmd.Env = rigEnv(t, home, "")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("a levelless row must refuse a level: %q", out)
		}
		if !strings.Contains(string(out), `local names no levels (models.json: "efforts")`) {
			t.Fatalf("the refusal must name the row and the field: %q", out)
		}
	})
}

func TestFlagPresenceWins(t *testing.T) {
	t.Run("an empty system flag is the choice", func(t *testing.T) {
		s := &bodySrv{}
		srv := newBodySrv(t, s)
		bin := buildBin(t, t.TempDir())
		cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1", "-system", "")
		cmdDir := t.TempDir()
		home := t.TempDir()
		cmd.Dir = cmdDir
		cmd.Env = rigEnv(t, home, "")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the run must succeed: %v\n%s", err, out)
		}
		want := sessionSection(cmdDir, home)
		if got := systemOf(t, s.last()); got != want {
			t.Fatalf("the system message = %q, want %q (-system \"\" drops the default; the session section stays)", got, want)
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
		cmd.Env = rigEnv(t, t.TempDir(), "")
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
	const target = "http://93.184.216.34/page"

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
			w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"id":"c1","type":"function","function":{"name":"web","arguments":"` +
				`{\"action\":\"fetch\",\"target\":\"` + target + `\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n"))
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
		cmd.Env = append(rigEnv(t, scratch, ""), envExtra...)
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
			Name: "swap", Prompt: "say hi", Cron: "0 5 * * *",
			Cwd: workDir, Model: "local", Busy: "skip",
		}, "/ws/swap", "sess-swap", bin+" run-job", cfgDir(t, scratch), fixedNow)
		if err != nil {
			t.Fatalf("create: %v (%s)", err, reply)
		}
		return "j1", workDir
	}
	fire := func(t *testing.T, binDir, bin, scratch, key, workDir, swapURL string) {
		t.Helper()
		sp := filepath.Join(scratch, "spool")
		if err := os.WriteFile(sp,
			[]byte("0 5 * * * "+bin+" run-job # rig-scheduler:"+sched.TagHome(cfgDir(t, scratch))+":"+key+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bin, "run-job", key)
		cmd.Dir = workDir
		cmd.Env = rigEnv(t, scratch, binDir, "RIG_SWAP_URL="+swapURL)
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
		bin := filepath.Join(t.TempDir(), "rig")
		scratch := t.TempDir()
		key, _ := mkJob(t, scratch, bin)
		var dialedMu sync.Mutex
		var dialed []string
		fetch := func(url string) (json.RawMessage, error) {
			dialedMu.Lock()
			dialed = append(dialed, url)
			dialedMu.Unlock()
			switch {
			case strings.HasSuffix(url, "/v1/models"):
				return json.RawMessage(`{"data":[]}`), nil
			case strings.HasSuffix(url, "/running"):
				return json.RawMessage(`{"running":[]}`), nil
			}
			return nil, errors.New("unexpected url " + url)
		}
		var workerArgv []string
		spawn := func(_ context.Context, argv []string, _ string, _ []string, _ func([]byte)) (sched.SpawnResult, error) {
			workerArgv = argv
			return sched.SpawnResult{Exit: 0}, nil
		}
		ct := newFakeCrontab()
		ct.Install(sched.LineFor(key, "0 5 * * *", bin+" run-job", cfgDir(t, scratch)))
		err := sched.RunJob(key, sched.RunOpts{
			Home:      filepath.Join(cfgDir(t, scratch), "scheduler"),
			Crontab:   ct,
			Fetch:     fetch,
			Spawn:     spawn,
			WorkerCmd: []string{bin},
			SwapURL:   "",
			Sandbox:   "off",
			RigHome:   cfgDir(t, scratch),
			Now:       fixedNow,
		})
		if err != nil {
			t.Fatalf("run-job: %v", err)
		}
		var sawDefault bool
		for _, u := range dialed {
			if strings.HasPrefix(u, "http://127.0.0.1:8090/") {
				sawDefault = true
			}
		}
		if !sawDefault {
			t.Fatalf("the embedded swapUrl did not receive the busy check (dials: %v)", dialed)
		}
		base := ""
		for i, a := range workerArgv {
			if a == "-base-url" && i+1 < len(workerArgv) {
				base = workerArgv[i+1]
			}
		}
		if base != "http://127.0.0.1:8090/v1" {
			t.Fatalf("the worker's -base-url = %q, want the embedded default", base)
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
		Name: "agents", Prompt: "say hi", Cron: "0 5 * * *",
		Cwd: workDir, Model: "local", Busy: "skip",
	}, workDir, "sess-agents", bin+" run-job", cfgDir(t, scratch), fixedNow)
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	key := "j1"
	if err := os.WriteFile(filepath.Join(scratch, "spool"),
		[]byte("0 5 * * * "+bin+" run-job # rig-scheduler:"+sched.TagHome(cfgDir(t, scratch))+":"+key+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sandboxOff(t, scratch)
	cmd := exec.Command(bin, "run-job", key)
	cmd.Dir = sessDir
	cmd.Env = append(rigEnv(t, scratch, binDir), "RIG_SWAP_URL="+srv.URL)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("run-job: %v\n%s", runErr, out)
	}
	sysMu.Lock()
	sys := workerSystem
	sysMu.Unlock()
	const defaultSystem = "you are an agent operating in rig, a minimal, general purpose harness, designed to help you get more done with less friction. you act on the session's workspace, using the available tools to inspect, change, and run things in it. the toolset is focused on purpose, with each tool's description saying when to use it. do not attempt to use a tool that does not exist in rig. the harness has guards: an allowlist, a retry guard (three identical failing calls to one tool in a turn exhaust the bound; a corrected call always executes), an approval gate, a plugin landing zone. every refusal names its rule and is there to guide you, not punish you. a refusal is final for that call: change the call or ask, never reach the same effect through another tool. when a tool fails, read the error and work out why before calling again. do not retry blindly, and stop when the environment or the plan is wrong. a capability you build twice belongs in a plugin. for any job of three or more steps, or one that touches several files, plan it in todo before the first edit: create the tasks, start one before working on it, complete or fail it when done, and leave the queue empty at the end. when the work is done, answer in plain text: what changed, what you verified, what is left."
	want := defaultSystem + "\n\n" + sessionSection(workDir, scratch) + "\n\n" + global + "\n\n" + jobAgents
	if sys != want {
		t.Fatalf("the worker's system message = %q, want the default plus the session section, the global and the job cwd's AGENTS.md (not the session's):\n%q", sys, want)
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
	writeModelRows(t, dir, `{"id": "local", "window": 32768, "maxTokens": 8192, "reserve": 8192, "keepRecent": 16384}`)
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

func TestDefaultJobModelLegacyKeyIsNamedAtStart(t *testing.T) {
	s := &bodySrv{}
	srv := newBodySrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	dir := cfgDir(t, scratch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(`{"defaultJobModel": "local"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(t, scratch, "")
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("the mint must not break the run: %v\n%s", runErr, out)
	}
	if got := s.count(); got != 1 {
		t.Fatalf("the minted run must make exactly one model call, got %d", got)
	}
	want := `defaultJobModel moved to model — the fleet is the resident model; delete the key`
	if !strings.Contains(string(out), want) {
		t.Fatalf("the legacy key must be named once at start: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "workers.json")); !os.IsNotExist(err) {
		t.Fatalf("nothing mints a workers.json anymore (stat err: %v)", err)
	}
}

func TestOneSlotWireRecordsTheMenuWithTheDrainPair(t *testing.T) {
	s := &bodySrv{}
	srv := newBodySrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(t, scratch, "", "RIG_SWAP_URL="+srv.URL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the run must succeed: %v\n%s", err, out)
	}
	if !hasToolName(s.last(), "scheduler") {
		t.Fatalf("the scheduler is wired everywhere: %v", toolNames(s.last()))
	}
	if !hasToolName(s.last(), "delegate") {
		t.Fatalf("a one-slot wire hosts the drain pair: %v", toolNames(s.last()))
	}
	for _, tl := range wireTools(t, s.last()) {
		if strings.Contains(tl.Description, "absent") || strings.Contains(tl.Description, "not wired") {
			t.Fatalf("the menu says nothing about what is absent: %s", tl.Name)
		}
	}
}

func TestTwoSlotWirePutsTheDrainPairOn(t *testing.T) {
	s := &bodySrv{slots: 2}
	srv := newBodySrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(t, scratch, "", "RIG_SWAP_URL="+srv.URL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the run must succeed: %v\n%s", err, out)
	}
	if !hasToolName(s.last(), "delegate") {
		t.Fatalf("a two-slot server hosts the fleet: %v", toolNames(s.last()))
	}
}

func TestWorkersFalseKeepsTheDrainPairOffAtTwoSlots(t *testing.T) {
	s := &bodySrv{slots: 2}
	srv := newBodySrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	dir := cfgDir(t, scratch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"workers": false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(t, scratch, "", "RIG_SWAP_URL="+srv.URL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the run must succeed: %v\n%s", err, out)
	}
	if hasToolName(s.last(), "delegate") {
		t.Fatalf("workers:false turns the drain pair off on a capable machine: %v", toolNames(s.last()))
	}
	if !hasToolName(s.last(), "scheduler") {
		t.Fatalf("the scheduler is wired everywhere: %v", toolNames(s.last()))
	}
}

func TestRemoteSessionRowWiresTheDrainPairWithoutSlots(t *testing.T) {
	s := &bodySrv{slots: 1}
	srv := newBodySrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	dir := cfgDir(t, scratch)
	remoteRow := `{"id": "brain", "window": 8192, "maxTokens": 1024, "reserve": 64, "keepRecent": 128, "remote": true, "baseUrl": "` + srv.URL + `/v1", "apiKey": "sk-test"}`
	writeModelRows(t, dir, remoteRow)
	cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(t, scratch, "", "RIG_SWAP_URL="+srv.URL, "RIG_MODEL=brain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the run must succeed: %v\n%s", err, out)
	}
	if !hasToolName(s.last(), "delegate") {
		t.Fatalf("a remote row runs its own parallelism: %v", toolNames(s.last()))
	}
}

func TestRetiredWorkersFileIsNamedOnceAtStart(t *testing.T) {
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	dir := cfgDir(t, scratch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workers.json"),
		[]byte(`{"model": "ghost", "slots": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-p", "hello")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(t, scratch, "")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "workers.json retired: the fleet is the resident model") {
		t.Fatalf("the retired file must be named once at start: %q", out)
	}
}

func TestSchedulerCreateWithoutAModelStoresTheUnnamedJob(t *testing.T) {
	dir := t.TempDir()
	if _, err := config.Load(dir, t.TempDir()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	home := t.TempDir()
	st := scratchStores(t, home, "/ws/default")
	ct := newFakeCrontab()
	tool := schedapi.New(st, ct, "rig run-job", "local", home)
	if !strings.Contains(string(tool.Schema()), "(default local when nothing is)") {
		t.Fatalf("the tool schema must name the session's model: %s", tool.Schema())
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
	if err := st.DB.QueryRow(`SELECT model FROM jobs WHERE id = 'j1'`).Scan(&m); err != nil {
		t.Fatal(err)
	}
	if m != "" {
		t.Fatalf("the job's model = %q, want empty (unnamed; the fire resolves it)", m)
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
	cmd.Env = rigEnv(t, scratch, "")
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
		value string
		voice string
	}{
		{"RIG_ROUNDS", "many", "rig: RIG_ROUNDS: expected an integer, got \"many\""},
		{"RIG_RESULT_CAP", "big", "rig: RIG_RESULT_CAP: expected an integer, got \"big\""},
		{"RIG_RETRIES", "three", "rig: RIG_RETRIES: expected an integer, got \"three\""},
	} {
		t.Run(tc.env, func(t *testing.T) {
			bin := buildBin(t, t.TempDir())
			cmd := exec.Command(bin, "-p", "hello")
			cmd.Dir = t.TempDir()
			env := rigEnv(t, t.TempDir(), "")
			env = append(env, tc.env+"="+tc.value)
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

func TestNegativeEnvBoundsRefuseLoud(t *testing.T) {
	for _, tc := range []struct {
		env   string
		voice string
	}{
		{"RIG_RETRIES=-3", "rig: RIG_RETRIES: expected a non-negative integer, got -3"},
		{"RIG_ROUNDS=-5", "rig: RIG_ROUNDS: expected a non-negative integer, got -5"},
		{"RIG_RESULT_CAP=-1", "rig: RIG_RESULT_CAP: expected a non-negative integer, got -1"},
	} {
		t.Run(tc.env, func(t *testing.T) {
			bin := buildBin(t, t.TempDir())
			cmd := exec.Command(bin, "-p", "hello")
			cmd.Dir = t.TempDir()
			env := rigEnv(t, t.TempDir(), "")
			env = append(env, tc.env)
			cmd.Env = env
			out, runErr := cmd.CombinedOutput()
			if runErr == nil {
				t.Fatalf("a negative %s must refuse: %q", tc.env, out)
			}
			if !strings.Contains(string(out), tc.voice) {
				t.Fatalf("the voice = %q, want %q", out, tc.voice)
			}
		})
	}
}

func TestModelsFileRowListsAndSwitches(t *testing.T) {
	dir := t.TempDir()
	writeModelRows(t, dir, localModelRow, `{"id": "brain", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768, "role": "worker"}`)
	cfg, err := config.Load(dir, t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	brain, ok := cfg.Models.Get("brain")
	if !ok {
		t.Fatal("the table has no brain row")
	}
	local, ok := cfg.Models.Get("local")
	if !ok {
		t.Fatal("the table has no local row")
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
	for _, n := range effectiveNativeNames() {
		if !allowed[n] {
			t.Errorf("native %q is not in the embedded allow default (no fleet: the worker tools stay out)", n)
		}
	}
	natives := map[string]bool{}
	for _, n := range nativeToolNames {
		natives[n] = true
	}
	natives["decide"] = true
	for _, n := range cfg.Settings.Allow {
		if !natives[n] {
			t.Errorf("embedded allow names %q, which is not a native", n)
		}
	}
}

func TestEmbeddedAllowGrowsWithTheFleet(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workers.json"),
		[]byte(`{"model": "local", "slots": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, n := range cfg.Settings.Allow {
		allowed[n] = true
	}
	for _, n := range []string{"scheduler", "delegate"} {
		if !allowed[n] {
			t.Errorf("worker tool %q is not in the allow default", n)
		}
	}
}

func TestOperatorAllowStandsAsWritten(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workers.json"),
		[]byte(`{"model": "local", "slots": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"allow": ["bash", "read"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"bash", "read"}; !reflect.DeepEqual(cfg.Settings.Allow, want) {
		t.Fatalf("the operator's allow stands as written: %v, want %v", cfg.Settings.Allow, want)
	}
}

func TestNoModelRefusesBeforeAnyRequest(t *testing.T) {
	s := &bodySrv{}
	srv := newBodySrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+scratch, "XDG_CONFIG_HOME="+scratch)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a run without a model must refuse: %s", out)
	}
	if !strings.Contains(string(out), "no model") {
		t.Fatalf("the refusal must name the missing model, got %s", out)
	}
	if s.count() != 0 {
		t.Fatalf("requests = %d, want 0 (the refusal precedes any call)", s.count())
	}
}

func TestNamedModelWithNoRowsRefusesBeforeAnyRequest(t *testing.T) {
	s := &bodySrv{}
	srv := newBodySrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = append(scrubSwap(os.Environ()),
		"HOME="+scratch,
		"XDG_CONFIG_HOME="+scratch,
		"RIG_MODEL=local",
		"RIG_SWAP_URL="+srv.URL,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a named model no file defines must refuse: %s", out)
	}
	if !strings.Contains(string(out), `no row for "local"`) {
		t.Fatalf("the refusal must name the missing row, got %q", out)
	}
	if s.count() != 0 {
		t.Fatalf("requests = %d, want 0 (the refusal precedes any call)", s.count())
	}
	if _, statErr := os.Stat(cfgDir(t, scratch)); !os.IsNotExist(statErr) {
		t.Fatalf("the refusal must precede the stores: the rig home %v (%v)", cfgDir(t, scratch), statErr)
	}
}

func TestSpawnedRigWithoutASwapFixtureNeverReadsTheDefaultSwapPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:8090")
	if err != nil {
		t.Skipf("8090 is already in use (%v); the box runs its own swap", err)
	}
	t.Cleanup(func() { listener.Close() })
	var dials int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
			atomic.AddInt32(&dials, 1)
		}
	}()

	s := &bodySrv{}
	srv := newBodySrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(t, scratch, "")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the run must succeed: %v\n%s", err, out)
	}
	if got := atomic.LoadInt32(&dials); got != 0 {
		t.Fatalf("the binary read the default swap port %d times, want never", got)
	}
	if hasToolName(s.last(), "delegate") {
		t.Fatalf("a closed swap wires the pair off, whatever answers the default port: %v", toolNames(s.last()))
	}
	if !hasToolName(s.last(), "scheduler") {
		t.Fatalf("the scheduler is wired everywhere: %v", toolNames(s.last()))
	}
}

func TestRigEnvPinsTheSwapUnlessTheTestNamesOne(t *testing.T) {
	t.Setenv("RIG_SWAP_URL", "http://127.0.0.1:8090")
	count := func(env []string) (int, string) {
		n := 0
		val := ""
		for _, kv := range env {
			if strings.HasPrefix(kv, "RIG_SWAP_URL=") {
				n++
				val = kv
			}
		}
		return n, val
	}
	n, val := count(rigEnv(t, t.TempDir(), ""))
	if n != 1 || val != "RIG_SWAP_URL="+testenv.ClosedSwapURL {
		t.Fatalf("the default env must carry exactly the closed port, got %d × %q", n, val)
	}
	n, val = count(rigEnv(t, t.TempDir(), "", "RIG_SWAP_URL=http://127.0.0.1:9"))
	if n != 1 || val != "RIG_SWAP_URL=http://127.0.0.1:9" {
		t.Fatalf("the test's own swap must win exactly once, got %d × %q", n, val)
	}
}

func TestTheProjectContractFollowsTheWorkspaceAtWire(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(a, "AGENTS.md"), []byte("CONTRACT-A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "AGENTS.md"), []byte("CONTRACT-B"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := testRoot(nullFrontend{})
	r.agents = "G"
	r.cwd = a
	wire(r)
	if !strings.Contains(r.fullSystem, "G\n\nCONTRACT-A") || strings.Contains(r.fullSystem, "CONTRACT-B") {
		t.Fatalf("the workspace's contract follows the operator's: %q", r.fullSystem)
	}
	r.cwd = b
	wire(r)
	if !strings.Contains(r.fullSystem, "CONTRACT-B") || strings.Contains(r.fullSystem, "CONTRACT-A") {
		t.Fatalf("a wire in another workspace reads that workspace's contract: %q", r.fullSystem)
	}
}
