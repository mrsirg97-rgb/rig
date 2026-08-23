package todo

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/scope"
)

const legacySchemaVersion = 1

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
