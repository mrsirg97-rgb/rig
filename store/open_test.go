package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/store/sqlx"
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

	if _, _, _, err := Open(path, probeDDL, 8); err == nil {
		t.Fatal("an upgrade (7 -> 8) with no migration must refuse")
	} else if !strings.Contains(err.Error(), "no migration") {
		t.Errorf("the refusal must name the missing migration: %v", err)
	}
	ran := 0
	if _, _, _, err := Open(path, probeDDL, 8, func(*sql.Tx, int, int) (string, error) { ran++; return "", nil }); err != nil {
		t.Fatalf("an upgrade (7 -> 8) with a migration must open: %v", err)
	}
	if ran != 1 {
		t.Fatalf("the migration ran %d times, want 1", ran)
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

func TestOpenQuickCheckSeesCorruptionTheSchemaReadMisses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "probe.sqlite")
	db, _, _, err := Open(path, probeDDL, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		if _, err := db.Exec("INSERT INTO probe (x) VALUES (?)", i); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.DB.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 8192 {
		t.Fatalf("fixture too small to carry a second page: %d bytes", len(raw))
	}
	corrupt := append([]byte{}, raw...)
	for i := 4096; i < 4200; i++ {
		corrupt[i] = 0xAB
	}
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	_, quarantined, _, err := Open(path, probeDDL, 1)
	if err != nil {
		t.Fatalf("open over page-level corruption: %v", err)
	}
	if quarantined == "" {
		t.Fatal("page-level corruption must quarantine (the schema-page read alone does not see it)")
	}
}

func TestOpenQuarantineMovesTheWalSidecarsToo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.sqlite")
	if err := os.WriteFile(path, []byte("definitely not a sqlite file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", []byte("stale wal frames"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-shm", []byte("stale shm"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, quarantined, _, err := Open(path, probeDDL, 1)
	if err != nil {
		t.Fatalf("open over quarantined corrupt file: %v", err)
	}
	defer db.DB.Close()
	if quarantined == "" {
		t.Fatal("corrupt file not quarantined and named")
	}
	if _, err := db.Exec("INSERT INTO probe (x) VALUES (1)"); err != nil {
		t.Fatalf("fresh file unusable: %v", err)
	}
}

func TestQuarantineMovesTheSidecarsBesideTheDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.sqlite")
	if err := os.WriteFile(path, []byte("main"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", []byte("stale wal frames"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-shm", []byte("stale shm"), 0o644); err != nil {
		t.Fatal(err)
	}
	aside := path + ".corrupt-1"
	if err := quarantine(path, aside); err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	for suffix, want := range map[string]string{"": "main", "-wal": "stale wal frames", "-shm": "stale shm"} {
		got, err := os.ReadFile(aside + suffix)
		if err != nil {
			t.Fatalf("the aside must carry the %s file beside the database: %v", suffix, err)
		}
		if string(got) != want {
			t.Fatalf("aside %s = %q, want %q", suffix, got, want)
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("the %s file must not stay at the original path (stat: %v)", path+suffix, err)
		}
	}
}

func TestQuarantineToleratesMissingSidecars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.sqlite")
	if err := os.WriteFile(path, []byte("main"), 0o644); err != nil {
		t.Fatal(err)
	}
	aside := path + ".corrupt-1"
	if err := quarantine(path, aside); err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	got, err := os.ReadFile(aside)
	if err != nil || string(got) != "main" {
		t.Fatalf("aside = %q, %v", got, err)
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
