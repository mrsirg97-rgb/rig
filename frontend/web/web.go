// Package web is the local dashboard (SPEC_SERVE): a reader of the rig
// home's stores, wired once at the root. It opens the same SQLite files a
// live session is writing (the same rig home, the same pragmas) and calls
// the same store verbs, rendered over loopback HTTP behind a minted token.
// One file plus a registration line at the root; the loop never names it.
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

// sessionName is the todo create's attribution (SPEC_SERVE 4): the event
// log carries it, the operator's create is distinguishable from the
// model's.
const sessionName = "dashboard"

// Options carries the dashboard's static inputs (the cwd-independent
// things): the rig home, the serve cwd, the models table, the crontab.
type Options struct {
	Home string
	CWD  string
	// Models is the models table from config (every row, the effort lists
	// and roles); an empty table is a valid "no models" page.
	Models models.Table
	// Crontab is the scheduler's crontab (the drift read); nil = the real
	// crontab (injected in tests).
	Crontab sched.Crontab
	// ReadTimeout bounds each read (and the one write); 0 = the default.
	ReadTimeout time.Duration
}

// Server is the dashboard: the token, the allowed origins, the store cache,
// and the static assets. It is built by New and served by
// ListenAndServe.
type Server struct {
	home    string
	cwd     string
	models  models.Table
	plugins []Plugin
	pending []Plugin
	crontab sched.Crontab
	readTO  time.Duration

	origins []string
	token   string
	stores  *storeCache
	static  fs.FS
}

// New builds the dashboard (SPEC_SERVE 1): it lists the plugins (the loaded
// set and the pending zone) and roots the static assets. It opens no store
// and mints no token; those happen on first use and at serve, respectively.
func New(opts Options) (*Server, error) {
	if opts.Home == "" {
		return nil, errors.New("web: home is required (the rig home)")
	}
	loaded, pending, err := listPlugins(opts.Home)
	if err != nil {
		return nil, fmt.Errorf("web: plugins: %w", err)
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("web: static: %w", err)
	}
	ct := opts.Crontab
	if ct == nil {
		ct = sched.RealCrontab("")
	}
	return &Server{
		home:    opts.Home,
		cwd:     opts.CWD,
		models:  opts.Models,
		plugins: loaded,
		pending: pending,
		crontab: ct,
		readTO:  opts.ReadTimeout,
		stores:  newStoreCache(opts.Home),
		static:  sub,
	}, nil
}

// Token returns the credential (minting it on first use), the one print.
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

// Handler is the full router: the token gate over the allow-list (SPEC_SERVE
// 5). It mints the token on first use.
func (s *Server) Handler() http.Handler {
	tok, _, err := s.Token()
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeErr(w, http.StatusInternalServerError, "serve token: "+err.Error())
		})
	}
	return gate(tok, s.router())
}

// ListenAndServe refuses a non-loopback bind by name, mints (and prints
// once) the token, sets the allowed origins from the bound address, and
// serves until ctx is done (SPEC_SERVE 5).
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

// Close releases the store cache (the open DBs).
func (s *Server) Close() error {
	s.stores.closeAll()
	return nil
}

// Loopback is the bind wall (SPEC_SERVE 5): the addr must name a loopback
// interface (127.0.0.1, ::1, or localhost); a non-loopback bind is refused
// by name (tailscale serve is the way out).
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
