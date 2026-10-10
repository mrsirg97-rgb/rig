package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mrsirg97-rgb/rig/v2/store"
)

// openStore is the one store open: the directory, the open, and the two
// stderr lines an open can produce (the quarantine and the migration
// report). The name is the store's own: it names the failure and the
// quarantined file.
func openStore(name, path string, statements []string, version int, migrate ...func(*sql.Tx, int, int) (string, error)) (store.DB, func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return store.DB{}, nil, err
	}
	db, quarantined, report, err := store.Open(path, statements, version, migrate...)
	if err != nil {
		return store.DB{}, nil, fmt.Errorf("%s store: %w", name, err)
	}
	if quarantined != "" {
		fmt.Fprintf(os.Stderr, "rig: quarantined corrupt %s file: %s\n", name, quarantined)
	}
	if report != "" {
		fmt.Fprintln(os.Stderr, "rig:", report)
	}
	return db, func() { db.DB.Close() }, nil
}
