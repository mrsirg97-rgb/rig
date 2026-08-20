package store

import (
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

func Open(path string, statements []string, wantVersion int) (sqlx.DB, string, error) {
	existing, _ := fileSize(path)
	var quarantined string
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := sql.Open("sqlite", path+"?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
		if err != nil {
			return sqlx.DB{}, quarantined, err
		}
		if err := integrity(raw); err != nil {
			_ = raw.Close()
			if attempt > 0 || existing == 0 {
				return sqlx.DB{}, quarantined, err
			}

			aside := fmt.Sprintf("%s.corrupt-%d", path, time.Now().UnixNano())
			if err := os.Rename(path, aside); err != nil {
				return sqlx.DB{}, quarantined, fmt.Errorf("quarantine: %w", err)
			}
			quarantined = aside
			existing = 0
			continue
		}
		if err := apply(raw, statements, wantVersion); err != nil {
			_ = raw.Close()
			return sqlx.DB{}, quarantined, err
		}
		return sqlx.DB{DB: raw}, quarantined, nil
	}
	return sqlx.DB{}, quarantined, fmt.Errorf("unreachable")
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

func apply(raw *sql.DB, statements []string, wantVersion int) error {
	if _, err := raw.Exec("CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		return fmt.Errorf("meta: %w", err)
	}
	row := raw.QueryRow("SELECT value FROM meta WHERE key='schema_version'")
	var stored string
	err := row.Scan(&stored)
	switch {
	case err == sql.ErrNoRows:
		if _, err := raw.Exec("INSERT INTO meta (key, value) VALUES ('schema_version', ?)", strconv.Itoa(wantVersion)); err != nil {
			return fmt.Errorf("schema_version: %w", err)
		}
	case err == nil:
		if stored != strconv.Itoa(wantVersion) {
			return fmt.Errorf("schema version mismatch: the file carries %s, this build wants %d", stored, wantVersion)
		}
	default:
		return fmt.Errorf("schema_version: %w", err)
	}
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
