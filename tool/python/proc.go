package python

import (
	"bufio"
	"encoding/json"
	"github.com/mrsirg97-rgb/rig/v3/tool/execwrap"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

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
	pending map[string]*pendingCall

	errMu  sync.Mutex
	errBuf []byte
}

type pendingCall struct {
	ch   chan Reply
	sink func(string)
}

func (k *kernel) start() (*proc, error) {
	argv := execwrap.Args([]string{k.python, k.host})
	cmd := exec.Command(argv[0], argv[1:]...)
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
		pending:  map[string]*pendingCall{},
	}
	k.proc = p
	go p.readLoop(stdout)
	go p.drainStderr(stderr)
	go k.waitLoop(p)
	return p, nil
}

func (p *proc) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), maxLine)
	for sc.Scan() {
		if line := sc.Text(); strings.TrimSpace(line) != "" {
			p.deliver(line)
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
	call := p.pending[*r.ID]
	if r.Stream != nil {
		p.mu.Unlock()
		if call != nil && call.sink != nil {
			call.sink(*r.Stream)
		}
		return
	}
	if call != nil {
		delete(p.pending, *r.ID)
	}
	p.mu.Unlock()
	if call != nil {
		select {
		case call.ch <- r:
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
