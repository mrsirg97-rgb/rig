package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mrsirg97-rgb/rig/store/sqlx"
)

type DB = sqlx.DB

var pragmas = []string{
	"PRAGMA journal_mode=WAL",
	"PRAGMA busy_timeout=5000",
	"PRAGMA foreign_keys=ON",
}

func Open(path string, statements []string, wantVersion int, migrate ...func(*sql.Tx, int, int) (string, error)) (sqlx.DB, string, string, error) {
	existing, _ := fileSize(path)
	var quarantined string
	for attempt := 0; attempt < 2; attempt++ {
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
			if err := os.Rename(path, aside); err != nil {
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
	return sqlx.DB{}, quarantined, "", fmt.Errorf("unreachable")
}

func migrateNow(raw *sql.DB, from, to int, migrate []func(*sql.Tx, int, int) (string, error)) (string, error) {
	if len(migrate) == 0 {
		return "", nil
	}
	tx, err := raw.BeginTx(context.Background(), nil)
	if err != nil {
		return "", fmt.Errorf("migration: %w", err)
	}
	report := ""
	for _, fn := range migrate {
		r, err := fn(tx, from, to)
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
	if from < to {
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

func integrity(raw *sql.DB) error {
	for _, p := range pragmas {
		if _, err := raw.Exec(p); err != nil {
			return fmt.Errorf("pragma %s: %w", p, err)
		}
	}

	if _, err := raw.Exec("SELECT count(*) FROM sqlite_master"); err != nil {
		return fmt.Errorf("integrity: %w", err)
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
