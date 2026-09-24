package todo

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/scope"
	tododdl "github.com/mrsirg97-rgb/rig/store/todo/ddl"
)

const legacySchemaVersion = 1

// EdgeMigration rebuilds the disposable task_deps projection with the
// edge kind column: requires and blocks live in one table, keyed by
// kind. The projection is rebuilt from the log inside every transaction
// and never trusted, so dropping it is safe; the log carries the edges.
func EdgeMigration(tx *sql.Tx, from, to int) (string, error) {
	if from >= 4 {
		return "", nil
	}
	if _, err := tx.Exec("DROP TABLE IF EXISTS task_deps"); err != nil {
		return "", fmt.Errorf("todo: migration: %w", err)
	}
	for _, stmt := range tododdl.Statements() {
		if strings.Contains(stmt, `"task_deps"`) {
			if _, err := tx.Exec(stmt); err != nil {
				return "", fmt.Errorf("todo: migration: %w", err)
			}
		}
	}
	return "todo migration: task_deps gained the edge kind", nil
}

// ReviewMigration pairs every historical complete event with an accept:
// complete now means active -> review, so without the pair a log written
// under the old semantics would replay every finished task as awaiting
// review. The accept follows its complete in event order (before any
// later prune that was meant to drop the row), the log is renumbered,
// and the pairing is a no-op once the store is at SchemaVersion 3.
func ReviewMigration(tx *sql.Tx, from, to int) (string, error) {
	if from >= 3 {
		return "", nil
	}
	rows, err := tx.Query("SELECT op, args, session, ts, scope FROM events ORDER BY seq")
	if err != nil {
		return "", fmt.Errorf("todo: migration: %w", err)
	}
	type row struct {
		op, args, ts, scope string
		session             sql.NullString
	}
	var log []row
	pairs := 0
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.op, &r.args, &r.session, &r.ts, &r.scope); err != nil {
			rows.Close()
			return "", fmt.Errorf("todo: migration: %w", err)
		}
		log = append(log, r)
		if r.op != "complete" {
			continue
		}
		var payload struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(r.args), &payload) == nil && payload.ID != "" {
			pairs++
			acceptArgs, _ := json.Marshal(map[string]any{"id": payload.ID})
			log = append(log, row{op: "accept", args: string(acceptArgs), ts: r.ts, scope: r.scope, session: r.session})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", fmt.Errorf("todo: migration: %w", err)
	}
	rows.Close()
	if pairs == 0 {
		return "", nil
	}
	if _, err := tx.Exec("DELETE FROM events"); err != nil {
		return "", fmt.Errorf("todo: migration: %w", err)
	}
	for i, r := range log {
		seq := int64(i + 1)
		var sess any
		if r.session.Valid {
			sess = r.session.String
		}
		if _, err := tx.Exec(
			"INSERT INTO events (seq, ts, op, args, session, scope) VALUES (?, ?, ?, ?, ?, ?)",
			seq, r.ts, r.op, r.args, sess, r.scope,
		); err != nil {
			return "", fmt.Errorf("todo: migration: %w", err)
		}
	}
	return fmt.Sprintf("todo migration: paired %d completed task%s", pairs, plural(pairs)), nil
}

var legacyStoreRe = regexp.MustCompile(`^([0-9a-f]{24})\.sqlite$`)

func LegacyScope(cwd string) string {
	sum := sha1.Sum([]byte(cwd))
	return hex.EncodeToString(sum[:12])
}

func Migration(cwd, dir string) func(*sql.Tx, int, int) (string, error) {
	return func(tx *sql.Tx, from, to int) (string, error) {
		folded, files, err := foldLegacy(tx, dir)
		if err != nil {
			return "", err
		}
		rescoped := 0
		oldScope := LegacyScope(cwd)
		newScope := scope.Key(cwd)
		if newScope != oldScope {
			var marker string
			err := tx.QueryRow(`SELECT value FROM meta WHERE key = ?`, "migrated:"+oldScope).Scan(&marker)
			if err == sql.ErrNoRows {
				rescoped, err = rescopeEvents(tx, oldScope, newScope)
				if err != nil {
					return "", err
				}
				if _, err := tx.Exec(`INSERT OR IGNORE INTO meta (key, value) VALUES (?, ?)`, "migrated:"+oldScope, newScope); err != nil {
					return "", fmt.Errorf("todo: migration: %w", err)
				}
			} else if err != nil {
				return "", fmt.Errorf("todo: migration: %w", err)
			}
		}
		var report []string
		if folded > 0 {
			report = append(report, fmt.Sprintf("folded %d events from %d store%s", folded, files, plural(files)))
		}
		if rescoped > 0 {
			report = append(report, fmt.Sprintf("re-scoped %d events", rescoped))
		}
		if len(report) > 0 {
			return "todo migration: " + strings.Join(report, ", "), nil
		}
		return "", nil
	}
}

func foldLegacy(tx *sql.Tx, dir string) (int, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("todo: migration: %w", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if legacyStoreRe.MatchString(e.Name()) {
			files = append(files, e.Name())
		}
	}
	if len(files) == 0 {
		return 0, 0, nil
	}
	sort.Strings(files)

	var seq int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM events`).Scan(&seq); err != nil {
		return 0, 0, fmt.Errorf("todo: migration: %w", err)
	}
	folded := 0
	for _, name := range files {
		hash := strings.TrimSuffix(name, ".sqlite")
		path := filepath.Join(dir, name)
		cdb, _, _, err := store.Open(path, legacyStatements(), legacySchemaVersion)
		if err != nil {
			return 0, 0, fmt.Errorf("todo: migration: %w", err)
		}
		_, cTx, err := cdb.TxReadOnly(context.Background())
		if err != nil {
			cdb.DB.Close()
			return 0, 0, fmt.Errorf("todo: migration: %w", err)
		}
		rows, err := cTx.Query(`SELECT seq, op, args, session, ts FROM events ORDER BY seq`)
		if err != nil {
			cTx.Rollback()
			cdb.DB.Close()
			return 0, 0, fmt.Errorf("todo: migration: %w", err)
		}
		for rows.Next() {
			var e eventRow
			var session sql.NullString
			if err := rows.Scan(&e.seq, &e.op, &e.args, &session, &e.ts); err != nil {
				rows.Close()
				cTx.Rollback()
				cdb.DB.Close()
				return 0, 0, fmt.Errorf("todo: migration: %w", err)
			}
			e.session = session.String
			seq++
			var sess *string
			if session.Valid {
				s := session.String
				sess = &s
			}
			if _, err := tx.Exec(`INSERT INTO events (seq, scope, ts, op, args, session) VALUES (?, ?, ?, ?, ?, ?)`,
				seq, hash, e.ts, e.op, e.args, sess); err != nil {
				rows.Close()
				cTx.Rollback()
				cdb.DB.Close()
				return 0, 0, fmt.Errorf("todo: migration: %w", err)
			}
			folded++
		}
		if err := rows.Err(); err != nil {
			cTx.Rollback()
			cdb.DB.Close()
			return 0, 0, fmt.Errorf("todo: migration: %w", err)
		}
		rows.Close()
		cTx.Rollback()
		cdb.DB.Close()

		for _, suffix := range []string{"", "-wal", "-shm"} {
			src := filepath.Join(dir, name+suffix)
			if _, err := os.Stat(src); err != nil {
				continue
			}
			if err := os.Rename(src, filepath.Join(dir, name+".migrated"+suffix)); err != nil {
				return 0, 0, fmt.Errorf("todo: migration: %w", err)
			}
		}
	}
	return folded, len(files), nil
}

func rescopeEvents(tx *sql.Tx, oldScope, newScope string) (int, error) {
	res, err := tx.Exec(`UPDATE events SET scope = ? WHERE scope = ?`, newScope, oldScope)
	if err != nil {
		return 0, fmt.Errorf("todo: migration: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("todo: migration: %w", err)
	}
	return int(n), nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func legacyStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS "events" (
  "seq" INTEGER NOT NULL,
  "args" TEXT NOT NULL,
  "op" TEXT NOT NULL,
  "session" TEXT,
  "ts" TEXT NOT NULL,
  PRIMARY KEY ("seq")
)`,
		`CREATE TABLE IF NOT EXISTS "meta" (
  "key" TEXT NOT NULL,
  "value" TEXT NOT NULL,
  PRIMARY KEY ("key")
)`,
	}
}
