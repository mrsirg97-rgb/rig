package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/mrsirg97-rgb/rig/models"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
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
	stores  *storeCache
	static  fs.FS
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
		root, _ = os.UserHomeDir()
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
		stores:    newStoreCache(opts.Home),
		static:    sub,
	}, nil
}

func (s *Server) Token() (string, bool, error) {
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
	return gate(tok, s.router())
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
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
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
