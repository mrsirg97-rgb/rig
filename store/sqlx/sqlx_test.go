package sqlx_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
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
	held := make(chan struct{})
	release := make(chan struct{})
	go func() {
		close(held)
		<-release
		_ = htx.Commit()
	}()

	contender, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		<-held
		close(started)
		_, tx, err := sqlx.DB{DB: contender}.Tx(context.Background())
		if err == nil {
			tx.Rollback()
		}
		done <- err
	}()
	<-started
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("the transaction must wait out the write lock, got %v", err)
	}
}

func TestTxBusyRefusalNamesTheWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.sqlite")
	dsn := path + "?_txlock=immediate&_pragma=busy_timeout(300)&_pragma=journal_mode(WAL)"
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
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, tx, err := sqlx.DB{DB: contender}.Tx(ctx)
	if err == nil {
		tx.Rollback()
		t.Fatal("a held write lock must refuse at the caller's deadline")
	}
	if !strings.Contains(err.Error(), "database busy") || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("the refusal must name the busy wait and the deadline, got %v", err)
	}
}
