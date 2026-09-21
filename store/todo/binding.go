package todo

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/scope"
)

// ProjectOf resolves a directory to the queue it owns: the repo's scope
// inside a repo (so two worktrees share one queue), the directory's own
// hash outside one. The path is made absolute first: a bucket keyed from
// "." and one keyed from its absolute path must be the same bucket, since
// the caller spells the path and the log cannot hold two spellings of one
// place. The label says which kind of bucket it is: a cwd-hash bucket is a
// place, not a project, and every reply that names it says so.
func ProjectOf(dir string) Project {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	ident := scope.Path(dir)
	if !scope.InRepo(dir) {
		return Project{Key: scope.ShortHash(ident), Label: scope.Label(ident), OutsideRepo: true}
	}
	// Inside a repo the queue is the repo's and the name says which repo:
	// the common dir's own base, so a session in a subdirectory or a second
	// worktree names the project it is in, not the folder it started in.
	return Project{Key: scope.ShortHash(ident), Label: repoName(ident)}
}

func repoName(commonDir string) string {
	name := filepath.Base(filepath.Dir(commonDir))
	if name == "." || name == "" || name == string(filepath.Separator) {
		return "repo"
	}
	return name
}

// RealSession reports whether a session id can hold a binding. The
// anonymous attribution a threadless call gets is shared by every
// anonymous caller, so a binding recorded under it would leak one
// session's project onto another's.
func RealSession(session string) bool {
	return session != "" && session != anon
}

// Binding is the queue a session works in: which scope its bare verbs
// act on, recorded so a resume from another directory lands in the same
// queue instead of re-deriving one from where the process happened to
// start. Mutable state beside the log, like meta: the log is the queue's
// spine, the binding only says which spine a session is reading.
type Binding struct {
	Session     string
	Scope       string
	Label       string
	OutsideRepo bool
}

// Bind records (or moves) a session's queue. An unattributable session
// has nowhere to record it and binds nothing: the call is inert, not an
// error, so an unthreaded verb still works, it just cannot carry a
// binding forward.
func Bind(ctx context.Context, db store.DB, b Binding) error {
	if !RealSession(b.Session) {
		return nil
	}
	if b.Scope == "" {
		return fmt.Errorf("todo: bind: empty scope")
	}
	notRepo := 0
	if b.OutsideRepo {
		notRepo = 1
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO session_project (session_id, scope, label, outside_repo, bound_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET scope = excluded.scope, label = excluded.label,
		 	outside_repo = excluded.outside_repo, bound_at = excluded.bound_at`,
		b.Session, b.Scope, b.Label, notRepo, nowRFC3339())
	if err != nil {
		return fmt.Errorf("todo: bind: %w", err)
	}
	return nil
}

// BindingOf reads a session's queue, if it has one.
func BindingOf(ctx context.Context, db store.DB, session string) (Binding, bool, error) {
	if !RealSession(session) {
		return Binding{}, false, nil
	}
	var (
		b       Binding
		notRepo int
	)
	err := db.QueryRowContext(ctx,
		`SELECT scope, label, outside_repo FROM session_project WHERE session_id = ?`, session).
		Scan(&b.Scope, &b.Label, &notRepo)
	if err == sql.ErrNoRows {
		return Binding{}, false, nil
	}
	if err != nil {
		return Binding{}, false, fmt.Errorf("todo: binding: %w", err)
	}
	b.Session = session
	b.OutsideRepo = notRepo != 0
	return b, true, nil
}

// Project returns the bound queue as the identity the verbs take.
func (b Binding) Project() Project {
	return Project{Key: b.Scope, Label: b.Label, OutsideRepo: b.OutsideRepo}
}
