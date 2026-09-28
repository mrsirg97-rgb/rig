package rem

import (
	"database/sql"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
	"path/filepath"
)

func Migration(cwd string) func(*sql.Tx, int, int) (string, error) {
	return func(db *sql.Tx, from, to int) (string, error) {
		removed := 0
		if from == 1 && to > from {
			n, err := removeCompactionRows(db)
			if err != nil {
				return "", err
			}
			removed = n
		}
		if from < 4 && columnExists(db, "content_md5") {
			if _, err := db.Exec(`ALTER TABLE memories RENAME COLUMN content_md5 TO content_sha256`); err != nil {
				return "", fmt.Errorf("rem: migration: %w", err)
			}
		}
		rehashed := 0
		if from < 3 {
			n, err := rehashDigests(db)
			if err != nil {
				return "", err
			}
			rehashed = n
		}
		rescoped := 0
		oldScope := scope.ShortHash(cwd)
		newScope := scopeKey(cwd)
		if newScope != oldScope {
			var marker string
			err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, "migrated:"+oldScope).Scan(&marker)
			if err == sql.ErrNoRows {
				n, err := rescopeRows(db, oldScope, newScope, filepath.Base(cwd))
				if err != nil {
					return "", err
				}
				rescoped = n
				if _, err := db.Exec(`INSERT OR IGNORE INTO meta (key, value) VALUES (?, ?)`, "migrated:"+oldScope, newScope); err != nil {
					return "", fmt.Errorf("rem: migration: %w", err)
				}
			} else if err != nil {
				return "", fmt.Errorf("rem: migration: %w", err)
			}
		}
		if removed > 0 || rescoped > 0 || rehashed > 0 {
			return fmt.Sprintf("rem migration: removed %d compaction reflections, re-scoped %d memories, rehashed %d digests", removed, rescoped, rehashed), nil
		}
		return "", nil
	}
}

func columnExists(db *sql.Tx, column string) bool {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('memories') WHERE name = ?`, column).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

func rehashDigests(db *sql.Tx) (int, error) {
	rows, err := db.Query(`SELECT id, content FROM memories WHERE length(content_sha256) != 64`)
	if err != nil {
		return 0, fmt.Errorf("rem: migration: %w", err)
	}
	type row struct {
		id      int64
		content string
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.content); err != nil {
			rows.Close()
			return 0, fmt.Errorf("rem: migration: %w", err)
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("rem: migration: %w", err)
	}
	rows.Close()
	for _, r := range list {
		if _, err := db.Exec(`UPDATE memories SET content_sha256 = ? WHERE id = ?`, sha256hex(r.content), r.id); err != nil {
			return 0, fmt.Errorf("rem: migration: %w", err)
		}
	}
	return len(list), nil
}

func removeCompactionRows(db *sql.Tx) (int, error) {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM memories WHERE source = 'session compaction'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("rem: migration: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM trigrams WHERE memory_id IN (SELECT id FROM memories WHERE source = 'session compaction')`); err != nil {
		return 0, fmt.Errorf("rem: migration: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM memory_fts WHERE rowid IN (SELECT id FROM memories WHERE source = 'session compaction')`); err != nil {
		return 0, fmt.Errorf("rem: migration: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM memories WHERE source = 'session compaction'`); err != nil {
		return 0, fmt.Errorf("rem: migration: %w", err)
	}
	return n, nil
}

func rescopeRows(db *sql.Tx, oldScope, newScope, label string) (int, error) {
	res, err := db.Exec(`UPDATE memories SET scope = ?, scope_label = ? WHERE scope = ?`, newScope, label, oldScope)
	if err != nil {
		return 0, fmt.Errorf("rem: migration: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rem: migration: %w", err)
	}
	return int(n), nil
}
