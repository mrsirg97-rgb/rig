package todo

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
)

func ProjectOf(dir string) Project {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	ident := scope.Path(dir)
	bare := scope.Bare(dir)
	if !scope.InRepo(dir) {
		return Project{Key: scope.ShortHash(ident), Label: scope.Label(ident), OutsideRepo: true}
	}
	return Project{Key: scope.ShortHash(ident), Label: repoName(ident, bare)}
}

func repoName(commonDir string, bare bool) string {
	dir := commonDir
	if !bare {
		dir = filepath.Dir(commonDir)
	}
	name := filepath.Base(dir)
	if name == "." || name == "" || name == string(filepath.Separator) {
		return "repo"
	}
	return name
}

func RealSession(session string) bool {
	return session != "" && session != anon
}

type Binding struct {
	Session     string
	Scope       string
	Label       string
	OutsideRepo bool
}

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

func (b Binding) Project() Project {
	return Project{Key: b.Scope, Label: b.Label, OutsideRepo: b.OutsideRepo}
}
