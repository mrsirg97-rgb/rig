package python

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/mrsirg97-rgb/rig/core"
)

//go:embed kernel_host.py
var kernelHostSrc string

const (
	defaultTimeoutMs = 120_000
	stderrTailLen    = 4096
	waitDelay        = 2 * time.Second
)

const description = "run Python in a persistent IPython kernel: variables, imports, and definitions persist " +
	"across calls; numpy and pandas are available."

const guidelines = "Guidelines: arithmetic, data shaping, parsing, bulk text -> compute here, never estimate; " +
	"compute once, query it in later calls. Reply: stdout and the last expression's value; action vars " +
	"lists the namespace, reset clears it. The kernel is born in the session's working directory."

const schemaJSON = `{
	"type": "object",
	"properties": {
		"code": {"type": "string", "description": "Python source to execute"},
		"action": {"type": "string", "enum": ["code", "vars", "reset"], "description": "'code' (or omitted) runs code; 'vars' summarises the namespace; 'reset' clears it."},
		"timeoutMs": {"type": "integer", "description": "Timeout in ms (default 120000)", "minimum": 1000}
	}
}`

type Reply struct {
	ID     *string `json:"id"`
	Ok     bool    `json:"ok"`
	Out    *string `json:"out"`
	Err    *string `json:"err"`
	Result *string `json:"result"`
	Error  *string `json:"error"`
	Note   *string `json:"note"`
}

type request struct {
	Code *string `json:"code,omitempty"`
	Cmd  *string `json:"cmd,omitempty"`
	ID   string  `json:"id"`
}

type given struct {
	Code      *string `json:"code"`
	Action    string  `json:"action"`
	TimeoutMs *int    `json:"timeoutMs"`
}

type Tool struct{ k *kernel }

var _ core.Tool = (*Tool)(nil)

func New() *Tool {
	return &Tool{k: &kernel{python: defaultInterpreter(), host: DefaultHost(), queue: make(chan struct{}, 1)}}
}

func NewWith(python, host string) *Tool {
	return &Tool{k: &kernel{python: python, host: host, queue: make(chan struct{}, 1), noBootstrap: true}}
}

func (t *Tool) Host() string { return t.k.host }

func (t *Tool) SetCwd(cwd string) { t.k.cwd = cwd }

func (t *Tool) Name() string { return "python" }

func (t *Tool) Description() string { return description + "\n\n" + guidelines }

func Guidelines() string { return guidelines }

func (t *Tool) Schema() json.RawMessage { return json.RawMessage(schemaJSON) }

func (t *Tool) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var a given
	if err := json.Unmarshal(data, &a); err != nil {
		return "", fmt.Errorf("python: %v", err)
	}
	var req request
	switch a.Action {
	case "", "code":
		if a.Code == nil || strings.TrimSpace(*a.Code) == "" {
			return "no code supplied", errors.New("no code supplied")
		}
		req.Code = a.Code
	case "vars", "reset":
		req.Cmd = &a.Action
	default:
		msg := fmt.Sprintf("python: unknown action %q; the actions are code (or omit it), vars, reset", a.Action)
		return msg, errors.New(msg)
	}
	timeoutMs := defaultTimeoutMs
	if a.TimeoutMs != nil {
		timeoutMs = *a.TimeoutMs
	}

	reply, err := t.k.send(ctx, req, timeoutMs)
	if err != nil {

		return "", err
	}
	text := render(reply)
	if reply.Ok {
		return text, nil
	}
	if reply.Error != nil && *reply.Error != "" {
		return text, errors.New(*reply.Error)
	}
	return text, errors.New(text)
}

func (t *Tool) Close() {
	p := t.k.shutdown()
	if p != nil {
		select {
		case <-p.dead:
		case <-time.After(waitDelay + time.Second):
		}
	}
}

func (t *Tool) Run(ctx context.Context, code string, timeoutMs int) (Reply, error) {
	req := request{Code: &code}
	return t.k.send(ctx, req, timeoutMs)
}

type kernel struct {
	python string
	host   string
	cwd    string

	queue chan struct{}

	seq atomic.Int64

	noBootstrap bool

	mu        sync.Mutex
	proc      *proc
	lastDeath *deathNote
}

type deathNote struct {
	desc   string
	stderr string
}

type proc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	readDone chan struct{}
	dead     chan struct{}

	mu      sync.Mutex
	pending map[string]chan Reply

	errMu  sync.Mutex
	errBuf []byte
}

func (k *kernel) start() (*proc, error) {
	cmd := exec.Command(k.python, k.host)
	cmd.Dir = k.cwd

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = waitDelay
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &proc{
		cmd:      cmd,
		stdin:    stdin,
		readDone: make(chan struct{}),
		dead:     make(chan struct{}),
		pending:  map[string]chan Reply{},
	}
	k.proc = p
	go p.readLoop(stdout)
	go p.drainStderr(stderr)
	go k.waitLoop(p)
	return p, nil
}

func (p *proc) readLoop(r io.Reader) {
	sc := bufio.NewReader(r)
	for {
		line, err := sc.ReadString('\n')
		if line != "" {
			p.deliver(line)
		}
		if err != nil {
			break
		}
	}
	close(p.readDone)
}

func (p *proc) deliver(raw string) {
	line := strings.TrimSpace(raw)
	if line == "" {
		return
	}
	var r Reply
	if err := json.Unmarshal([]byte(line), &r); err != nil {
		return
	}
	if r.ID == nil {
		return
	}
	p.mu.Lock()
	ch := p.pending[*r.ID]
	delete(p.pending, *r.ID)
	p.mu.Unlock()
	if ch != nil {
		select {
		case ch <- r:
		default:
		}
	}
}

func (p *proc) drainStderr(r io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			p.errMu.Lock()
			p.errBuf = append(p.errBuf, buf[:n]...)
			if len(p.errBuf) > stderrTailLen {
				p.errBuf = append([]byte(nil), p.errBuf[len(p.errBuf)-stderrTailLen:]...)
			}
			p.errMu.Unlock()
		}
		if err != nil {
			break
		}
	}
}

func (p *proc) stderrTail() string {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	return strings.TrimSpace(string(p.errBuf))
}

func (k *kernel) waitLoop(p *proc) {
	waitErr := p.cmd.Wait()
	<-p.readDone
	k.onDeath(p, waitErr)
	close(p.dead)
}

func (k *kernel) onDeath(p *proc, _ error) {
	desc := exitDescription(p.cmd.ProcessState)
	tail := p.stderrTail()

	k.mu.Lock()
	current := k.proc == p
	if current {
		k.proc = nil
		k.lastDeath = &deathNote{desc: desc, stderr: tail}
	}
	k.mu.Unlock()
	if !current {
		return
	}
	msg := "kernel exited (" + desc + ")"
	if tail != "" {
		msg += "\n[stderr]\n" + tail
	}
	p.failAll(msg)
}

func (p *proc) failAll(msg string) {
	p.mu.Lock()
	chs := make([]chan Reply, 0, len(p.pending))
	for id, ch := range p.pending {
		chs = append(chs, ch)
		delete(p.pending, id)
	}
	p.mu.Unlock()
	for _, ch := range chs {
		select {
		case ch <- Reply{ID: nil, Ok: false, Error: strPtr(msg)}:
		default:
		}
	}
}

func (k *kernel) send(ctx context.Context, req request, timeoutMs int) (Reply, error) {
	k.queue <- struct{}{}
	defer func() { <-k.queue }()

	var p *proc
	k.mu.Lock()
	p = k.proc
	k.mu.Unlock()

	var note *string
	if p == nil {
		note = k.takeDeathNote()
		if !k.noBootstrap {
			if err := ensureKernel(ctx); err != nil {
				msg := err.Error()
				return Reply{ID: nil, Ok: false, Error: &msg, Note: note}, nil
			}
		}
		var err error
		k.mu.Lock()
		p, err = k.start()
		k.mu.Unlock()
		if err != nil {
			msg := "kernel failed to start: " + err.Error()
			return Reply{ID: nil, Ok: false, Error: &msg, Note: note}, nil
		}
	}

	id := strconv.FormatInt(k.seq.Add(1), 10)
	req.ID = id
	ch := make(chan Reply, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()

	body, err := json.Marshal(req)
	if err != nil {
		p.forget(id)
		msg := "kernel is not writable: " + err.Error()
		return Reply{ID: strPtr(id), Ok: false, Error: &msg, Note: note}, nil
	}
	body = append(body, '\n')
	if _, err := p.stdin.Write(body); err != nil {

		p.forget(id)
		msg := "kernel is not writable: " + err.Error()
		return Reply{ID: strPtr(id), Ok: false, Error: &msg, Note: note}, nil
	}

	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case r := <-ch:
		if note != nil {
			r.Note = note
		}
		return r, nil
	case <-timer.C:
		msg := fmt.Sprintf("timed out after %ds; kernel will be restarted on the next call; all variables are gone. Re-run setup, or pass a larger timeoutMs.",
			roundSeconds(timeoutMs))
		k.restart()
		return Reply{ID: strPtr(id), Ok: false, Error: &msg, Note: note}, nil
	case <-ctx.Done():
		p.forget(id)
		return Reply{}, ctx.Err()
	}
}

func (p *proc) forget(id string) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

func (k *kernel) takeDeathNote() *string {
	k.mu.Lock()
	d := k.lastDeath
	k.lastDeath = nil
	k.mu.Unlock()
	if d == nil {
		return nil
	}
	s := fmt.Sprintf("note: fresh kernel; previous kernel exited (%s); all previous variables are gone", d.desc)
	if d.stderr != "" {
		s += "\n[stderr]\n" + d.stderr
	}
	return strPtr(s)
}

func (k *kernel) restart() { k.teardown("kernel was restarted; all variables are gone") }
func (k *kernel) shutdown() *proc {
	return k.teardown("kernel shut down")
}

func (k *kernel) teardown(msg string) *proc {
	k.mu.Lock()
	p := k.proc
	k.proc = nil
	k.mu.Unlock()
	if p == nil {
		return nil
	}
	p.failAll(msg)
	if p.cmd.Process != nil {
		syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
	return p
}

func roundSeconds(ms int) int {
	return (ms + 500) / 1000
}

func render(r Reply) string {
	var parts []string
	if r.Note != nil && strings.TrimSpace(*r.Note) != "" {
		parts = append(parts, strings.TrimRightFunc(*r.Note, unicode.IsSpace))
	}
	if r.Out != nil && strings.TrimSpace(*r.Out) != "" {
		parts = append(parts, strings.TrimRightFunc(*r.Out, unicode.IsSpace))
	}
	if r.Err != nil && strings.TrimSpace(*r.Err) != "" {
		parts = append(parts, "[stderr]\n"+strings.TrimRightFunc(*r.Err, unicode.IsSpace))
	}
	if r.Error != nil && *r.Error != "" {
		parts = append(parts, "[error]\n"+*r.Error)
	}
	if r.Result != nil && *r.Result != "" && (r.Out == nil || !strings.Contains(*r.Out, *r.Result)) {
		parts = append(parts, *r.Result)
	}
	if len(parts) == 0 {
		if r.Ok {
			return "(no output)"
		}
		return "(failed, no output)"
	}
	return strings.Join(parts, "\n")
}

func strPtr(s string) *string { return &s }

func exitDescription(st *os.ProcessState) string {
	if st == nil {
		return "code 1"
	}
	if ws, ok := st.Sys().(syscall.WaitStatus); ok {
		if ws.Signaled() {
			return "signal " + signalName(ws.Signal())
		}
		return fmt.Sprintf("code %d", ws.ExitStatus())
	}
	return fmt.Sprintf("code %d", st.ExitCode())
}

var signalNames = map[syscall.Signal]string{
	syscall.SIGHUP:    "SIGHUP",
	syscall.SIGINT:    "SIGINT",
	syscall.SIGQUIT:   "SIGQUIT",
	syscall.SIGILL:    "SIGILL",
	syscall.SIGTRAP:   "SIGTRAP",
	syscall.SIGABRT:   "SIGABRT",
	syscall.SIGBUS:    "SIGBUS",
	syscall.SIGFPE:    "SIGFPE",
	syscall.SIGKILL:   "SIGKILL",
	syscall.SIGUSR1:   "SIGUSR1",
	syscall.SIGSEGV:   "SIGSEGV",
	syscall.SIGUSR2:   "SIGUSR2",
	syscall.SIGPIPE:   "SIGPIPE",
	syscall.SIGALRM:   "SIGALRM",
	syscall.SIGTERM:   "SIGTERM",
	syscall.SIGCHLD:   "SIGCHLD",
	syscall.SIGCONT:   "SIGCONT",
	syscall.SIGSTOP:   "SIGSTOP",
	syscall.SIGTSTP:   "SIGTSTP",
	syscall.SIGTTIN:   "SIGTTIN",
	syscall.SIGTTOU:   "SIGTTOU",
	syscall.SIGURG:    "SIGURG",
	syscall.SIGXCPU:   "SIGXCPU",
	syscall.SIGXFSZ:   "SIGXFSZ",
	syscall.SIGVTALRM: "SIGVTALRM",
	syscall.SIGPROF:   "SIGPROF",
	syscall.SIGWINCH:  "SIGWINCH",
	syscall.SIGIO:     "SIGIO",
	syscall.SIGSYS:    "SIGSYS",
}

func signalName(s syscall.Signal) string {
	if n, ok := signalNames[s]; ok {
		return n
	}
	return "SIG" + strconv.Itoa(int(s))
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}

func defaultInterpreter() string {
	return filepath.Join(homeDir(), ".pi", "agent", "kernel-venv", "bin", "python")
}

func rigHome() string {
	if v := os.Getenv("RIG_HOME"); v != "" {
		return v
	}
	return filepath.Join(homeDir(), ".rig")
}

func DefaultHost() string {
	local := filepath.Join(homeDir(), ".pi", "agent", "kernel", "kernel_host.py")
	if _, err := os.Stat(local); err == nil {
		return local
	}
	dir := filepath.Join(rigHome(), "kernel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, "kernel_host.py")
	if existing, err := os.ReadFile(path); err != nil || string(existing) != kernelHostSrc {
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(kernelHostSrc), 0o644); err == nil {
			os.Rename(tmp, path)
		}
	}
	return path
}

var (
	bootMu       sync.Mutex
	bootInflight *bootCall
)

type bootCall struct {
	done chan struct{}
	err  error
}

func ensureKernel(ctx context.Context) error {
	if _, err := os.Stat(defaultInterpreter()); err == nil {
		return nil
	}
	bootMu.Lock()
	if b := bootInflight; b != nil {
		bootMu.Unlock()
		<-b.done
		return b.err
	}
	b := &bootCall{done: make(chan struct{})}
	bootInflight = b
	bootMu.Unlock()

	venvDir := filepath.Join(homeDir(), ".pi", "agent", "kernel-venv")
	var err error
	if err = runStep(ctx, "python3", []string{"-m", "venv", venvDir}); err == nil {
		err = runStep(ctx, filepath.Join(venvDir, "bin", "pip"), []string{"install", "--quiet", "ipython", "numpy", "pandas"})
	}
	if err != nil {
		err = fmt.Errorf("kernel bootstrap failed (needs python3 + network): %v", err)
	}
	b.err = err
	bootMu.Lock()
	if bootInflight == b && err != nil {
		bootInflight = nil
	}
	bootMu.Unlock()
	close(b.done)
	return err
}

func runStep(ctx context.Context, command string, args []string) error {
	stepCtx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(stepCtx, command, args...)
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	s := stderr.String()
	if s == "" {
		s = err.Error()
	}
	if len(s) > 500 {
		s = s[len(s)-500:]
	}
	first := ""
	if len(args) > 0 {
		first = args[0]
	}
	return fmt.Errorf("%s %s: %s", command, first, s)
}
