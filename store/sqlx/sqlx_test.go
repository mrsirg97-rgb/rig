package sqlx_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mrsirg97-rgb/rig/store/sqlx"
)

func TestTxWaitsOutTheWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.sqlite")
	dsn := path + "?_txlock=immediate&_pragma=busy_timeout(100)&_pragma=journal_mode(WAL)"
	holder, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	htx, err := holder.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatal(err)
	}
	defer htx.Rollback()

	contender, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()
	release := make(chan struct{})
	go func() {
		<-release
		_ = htx.Commit()
	}()
	done := make(chan error, 1)
	go func() {
		_, tx, err := sqlx.DB{DB: contender}.Tx(context.Background())
		if err == nil {
			tx.Rollback()
		}
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("the transaction must wait out the write lock, got %v", err)
	}
}
