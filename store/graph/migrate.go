package graph

import (
	"context"
	"database/sql"
	"fmt"
)

func Migration() func(*sql.Tx, int, int) (string, error) {
	return func(tx *sql.Tx, from, to int) (string, error) {
		if from >= 2 {
			return "", nil
		}
		if _, err := tx.Exec(`DELETE FROM symbol_fts`); err != nil {
			return "", fmt.Errorf("graph: migration: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM symbol_grams`); err != nil {
			return "", fmt.Errorf("graph: migration: %w", err)
		}
		rows, err := tx.QueryContext(context.Background(), `SELECT package, name, kind, file, line, end_line FROM symbols`)
		if err != nil {
			return "", fmt.Errorf("graph: migration: %w", err)
		}
		var symbols []Symbol
		for rows.Next() {
			var s Symbol
			if err := rows.Scan(&s.Package, &s.Name, &s.Kind, &s.File, &s.Line, &s.EndLine); err != nil {
				_ = rows.Close()
				return "", fmt.Errorf("graph: migration: %w", err)
			}
			symbols = append(symbols, s)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return "", fmt.Errorf("graph: migration: %w", err)
		}
		_ = rows.Close()
		for _, s := range symbols {
			if err := insertLexical(context.Background(), tx, s); err != nil {
				return "", err
			}
		}
		if len(symbols) > 0 {
			return fmt.Sprintf("graph migration: rebuilt the lexical tables from %d symbols", len(symbols)), nil
		}
		return "", nil
	}
}
