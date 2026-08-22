package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/store/sqlx"
)

var probeDDL = []string{"CREATE TABLE IF NOT EXISTS probe (x INTEGER PRIMARY KEY)"}

func TestOpenInitializesSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe.sqlite")
	db, quarantined, _, err := Open(path, probeDDL, 7)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if quarantined != "" {
		t.Fatalf("fresh open quarantined: %q", quarantined)
	}
	if _, err := db.Exec("SELECT COUNT(*) FROM probe"); err != nil {
		t.Fatalf("schema statements not applied: %v", err)
	}

	if _, _, _, err := Open(path, probeDDL, 7); err != nil {
		t.Fatalf("re-open same version: %v", err)
	}

	if _, _, _, err := Open(path, probeDDL, 8); err != nil {
		t.Fatalf("an upgrade (7 -> 8) must open: %v", err)
	}
	if _, _, _, err := Open(path, probeDDL, 7); err == nil {
		t.Fatal("version mismatch not refused")
	} else if !strings.Contains(err.Error(), "8") || !strings.Contains(err.Error(), "7") {
		t.Errorf("mismatch did not name both versions: %v", err)
	}
}

func TestOpenQuarantinesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.sqlite")
	if err := os.WriteFile(path, []byte("definitely not a sqlite file"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, quarantined, _, err := Open(path, probeDDL, 1)
	if err != nil {
		t.Fatalf("open over quarantined corrupt file: %v", err)
	}
	if quarantined == "" {
		t.Fatal("corrupt file not quarantined and named")
	}
	if !strings.HasPrefix(quarantined, path+".corrupt-") {
		t.Errorf("quarantine aside not named from the original path: %q", quarantined)
	}
	if _, err := os.Stat(quarantined); err != nil {
		t.Errorf("quarantined aside missing: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("fresh file not created: %v", err)
	}
	if _, err := db.Exec("INSERT INTO probe (x) VALUES (1)"); err != nil {
		t.Fatalf("fresh file unusable: %v", err)
	}
}

func TestTxFromFailsClosed(t *testing.T) {
	if _, err := sqlx.TxFrom(context.Background()); err == nil {
		t.Fatal("TxFrom without a transaction succeeded")
	}
	path := filepath.Join(t.TempDir(), "tx.sqlite")
	db, _, _, err := Open(path, probeDDL, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, tx, err := db.Tx(context.Background())
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	defer tx.Rollback()
	if got, err := sqlx.TxFrom(ctx); err != nil || got != tx {
		t.Fatalf("TxFrom inside the transaction: %v %p", err, got)
	}
}
