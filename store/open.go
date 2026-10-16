package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mrsirg97-rgb/rig/v3/store/sqlx"
)

type DB = sqlx.DB

func Open(path string, statements []string, wantVersion int, migrate ...func(*sql.Tx, int, int) (string, error)) (sqlx.DB, string, string, error) {
	if strings.Contains(path, "?") {
		return sqlx.DB{}, "", "", fmt.Errorf("store path %s carries '?': the driver would split the DSN at it and open a different file — rename the store", path)
	}
	existing, _ := fileSize(path)
	var quarantined string
	for attempt := 0; ; attempt++ {
		raw, err := sql.Open("sqlite", path+"?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
		if err != nil {
			return sqlx.DB{}, quarantined, "", err
		}
		if err := integrity(raw); err != nil {
			_ = raw.Close()
			if attempt > 0 || existing == 0 {
				return sqlx.DB{}, quarantined, "", err
			}

			aside := fmt.Sprintf("%s.corrupt-%d", path, time.Now().UnixNano())
			if err := quarantine(path, aside); err != nil {
				return sqlx.DB{}, quarantined, "", fmt.Errorf("quarantine: %w", err)
			}
			quarantined = aside
			existing = 0
			continue
		}
		from, err := readVersion(raw, wantVersion)
		if err != nil {
			_ = raw.Close()
			return sqlx.DB{}, quarantined, "", err
		}
		if from > wantVersion {
			_ = raw.Close()
			return sqlx.DB{}, quarantined, "", fmt.Errorf("schema version mismatch: the file carries %d, this build wants %d", from, wantVersion)
		}
		if from < wantVersion && len(migrate) == 0 {
			_ = raw.Close()
			return sqlx.DB{}, quarantined, "", fmt.Errorf("schema version mismatch: the file carries %d, this build wants %d and carries no migration", from, wantVersion)
		}
		if err := apply(raw, statements); err != nil {
			_ = raw.Close()
			return sqlx.DB{}, quarantined, "", err
		}
		report, err := migrateNow(raw, from, wantVersion, migrate)
		if err != nil {
			_ = raw.Close()
			return sqlx.DB{}, quarantined, "", err
		}
		return sqlx.DB{DB: raw}, quarantined, report, nil
	}
}

func quarantine(path, aside string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		src := path + suffix
		if _, err := os.Stat(src); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("stat %s: %w", src, err)
			}
			continue
		}
		if err := os.Rename(src, aside+suffix); err != nil {
			return err
		}
	}
	return nil
}

func migrateNow(raw *sql.DB, from, to int, migrate []func(*sql.Tx, int, int) (string, error)) (string, error) {
	if len(migrate) == 0 {
		return "", nil
	}
	tx, err := raw.BeginTx(context.Background(), nil)
	if err != nil {
		return "", fmt.Errorf("migration: %w", err)
	}
	current, err := versionIn(tx)
	if err != nil {
		_ = tx.Rollback()
		return "", err
	}
	if current > from && current >= to {
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("migration: %w", err)
		}
		return "", nil
	}
	report := ""
	for _, fn := range migrate {
		r, err := fn(tx, current, to)
		if err != nil {
			_ = tx.Rollback()
			return "", err
		}
		if r != "" {
			if report != "" {
				report += "\n"
			}
			report += r
		}
	}
	if current < to {
		if _, err := tx.Exec(`UPDATE meta SET value = ? WHERE key = 'schema_version'`, strconv.Itoa(to)); err != nil {
			_ = tx.Rollback()
			return "", fmt.Errorf("schema_version: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("migration: %w", err)
	}
	return report, nil
}

func versionIn(tx *sql.Tx) (int, error) {
	var stored string
	err := tx.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&stored)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("schema_version: the row vanished before the migration transaction")
	}
	if err != nil {
		return 0, fmt.Errorf("schema_version: %w", err)
	}
	v, err := strconv.Atoi(stored)
	if err != nil {
		return 0, fmt.Errorf("schema_version: %w", err)
	}
	return v, nil
}

func integrity(raw *sql.DB) error {
	rows, err := raw.Query("PRAGMA quick_check")
	if err != nil {
		return fmt.Errorf("integrity: quick_check: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var msg string
		if err := rows.Scan(&msg); err != nil {
			return fmt.Errorf("integrity: quick_check: %w", err)
		}
		if msg != "ok" {
			return fmt.Errorf("integrity: quick_check: %s", msg)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("integrity: quick_check: %w", err)
	}
	return nil
}

func readVersion(raw *sql.DB, wantVersion int) (int, error) {
	if _, err := raw.Exec("CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		return 0, fmt.Errorf("meta: %w", err)
	}
	row := raw.QueryRow("SELECT value FROM meta WHERE key='schema_version'")
	var stored string
	err := row.Scan(&stored)
	switch {
	case err == sql.ErrNoRows:
		if _, err := raw.Exec("INSERT OR IGNORE INTO meta (key, value) VALUES ('schema_version', ?)", strconv.Itoa(wantVersion)); err != nil {
			return 0, fmt.Errorf("schema_version: %w", err)
		}
		return wantVersion, nil
	case err == nil:
		v, perr := strconv.Atoi(stored)
		if perr != nil {
			return 0, fmt.Errorf("schema_version: %w", perr)
		}
		return v, nil
	default:
		return 0, fmt.Errorf("schema_version: %w", err)
	}
}

func apply(raw *sql.DB, statements []string) error {
	for _, s := range statements {
		if _, err := raw.Exec(s); err != nil {
			return fmt.Errorf("schema: %w", err)
		}
	}
	return nil
}

func fileSize(path string) (int64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return fi.Size(), true
}
