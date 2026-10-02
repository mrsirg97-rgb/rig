package decision

import (
	"database/sql"
	"fmt"
)

func Migration() func(*sql.Tx, int, int) (string, error) {
	return func(tx *sql.Tx, from, to int) (string, error) {
		if from < 2 && !columnExists(tx, "unsure") {
			if _, err := tx.Exec(`ALTER TABLE "decisions" ADD COLUMN "unsure" INTEGER NOT NULL DEFAULT 0`); err != nil {
				return "", fmt.Errorf("decision: migration: %w", err)
			}
			return "decision migration: added unsure", nil
		}
		return "", nil
	}
}

func columnExists(tx *sql.Tx, column string) bool {
	rows, err := tx.Query(`SELECT name FROM pragma_table_info('decisions')`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}
