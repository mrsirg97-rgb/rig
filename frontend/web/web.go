package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/models"
	"github.com/mrsirg97-rgb/rig/v3/room"
	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
)

const sessionName = "dashboard"

type Options struct {
	Home string
	CWD  string

	Models models.Table

	Crontab sched.Crontab

	RunnerCmd string

	ReadTimeout time.Duration

	Natives []string

	Root string

	Commands []core.Command
	Env      any
	Status   func(context.Context) Status

	Machine room.Member
	Door    room.Door
	Tailnet string
	Version string
	Ready   func(ctx context.Context, m models.Model) error

	StopWorkers func()
}

type Server struct {
	home      string
	cwd       string
	models    models.Table
	crontab   sched.Crontab
	runnerCmd string
	readTO    time.Duration
	natives   map[string]bool
	root      string

	origins []string
	token   string
	tokenMu sync.Mutex
	stores  *storeCache
	static  fs.FS
	chat    *chat
	status  func(context.Context) Status
	machine room.Member
	door    room.Door
	tailnet string
	version string
	ready   func(ctx context.Context, m models.Model) error
}

func New(opts Options) (*Server, error) {
	if opts.Home == "" {
		return nil, errors.New("web: home is required (the rig home)")
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("web: static: %w", err)
	}
	ct := opts.Crontab
	if ct == nil {
		ct = sched.RealCrontab("")
	}
	runner := opts.RunnerCmd
	if runner == "" {
		runner = "rig run-job"
	}
	natives := make(map[string]bool, len(opts.Natives))
	for _, n := range opts.Natives {
		natives[n] = true
	}
	root := opts.Root
	if root == "" {
		dir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("web: the folder browser's root: %v", err)
		}
		root = dir
	}
	return &Server{
		home:      opts.Home,
		cwd:       opts.CWD,
		models:    opts.Models,
		crontab:   ct,
		runnerCmd: runner,
		readTO:    opts.ReadTimeout,
		natives:   natives,
		root:      root,
		stores:    newStoreCache(opts.Home, ct, runner),
		static:    sub,
		chat:      newChat(opts.Commands, opts.Env, opts.StopWorkers),
		status:    opts.Status,
		machine:   opts.Machine,
		door:      opts.Door,
		tailnet:   opts.Tailnet,
		ready:     opts.Ready,
		version:   opts.Version,
	}, nil
}

func (s *Server) Token() (string, bool, error) {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	if s.token != "" {
		return s.token, false, nil
	}
	tok, minted, err := EnsureToken(s.home)
	if err != nil {
		return "", false, err
	}
	s.token = tok
	return tok, minted, nil
}

func (s *Server) Handler() http.Handler {
	tok, _, err := s.Token()
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeErr(w, http.StatusInternalServerError, "serve token: "+err.Error())
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wirePath(r.URL.Path) {
			s.handleWire(w, r, r.URL.Path)
			return
		}
		gate(tok, s.router()).ServeHTTP(w, r)
	})
}

func (s *Server) WireHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !modelPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		s.handleWire(w, r, r.URL.Path)
	})
}

func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	if err := Loopback(addr); err != nil {
		return err
	}
	tok, minted, err := s.Token()
	if err != nil {
		return err
	}
	if minted {
		fmt.Fprintf(os.Stderr, "rig serve: minted a new token (printed once): %s\n", tok)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("serve: addr %q: %v", addr, err)
	}
	s.origins = []string{"http://" + host + ":" + port}
	if isLoopbackHost(host) {
		s.origins = append(s.origins, "http://localhost:"+port)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	var tln net.Listener
	if s.tailnet != "" && s.tailnet != addr {
		tln, err = net.Listen("tcp", s.tailnet)
		if err != nil {
			ln.Close()
			return fmt.Errorf("serve: the tailnet bind %s: %v", s.tailnet, err)
		}
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	var tailSrv *http.Server
	errCh := make(chan error, 2)
	go func() {
		errCh <- srv.Serve(ln)
	}()
	if tln != nil {
		tail := &http.Server{
			Handler:           s.WireHandler(),
			ReadHeaderTimeout: 5 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		tailSrv = tail
		go func() {
			errCh <- tail.Serve(tln)
		}()
	}
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		if tailSrv != nil {
			_ = tailSrv.Shutdown(shutdownCtx)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) Close() error {
	s.stores.closeAll()
	return nil
}

func Loopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("serve: addr %q: %v", addr, err)
	}
	if !isLoopbackHost(host) {
		return fmt.Errorf("serve: %q is not a loopback address (127.0.0.1, ::1, or localhost); tailscale serve is the way out", addr)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
