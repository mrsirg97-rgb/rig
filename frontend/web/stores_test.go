package web

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

func TestSchedulerStoreMigratesThroughTheServersCrontab(t *testing.T) {
	home := t.TempDir()
	shome := filepath.Join(home, "scheduler")
	if err := os.MkdirAll(shome, 0o755); err != nil {
		t.Fatal(err)
	}
	scdb, _, _, err := store.Open(filepath.Join(shome, "global.sqlite"), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Create(context.Background(), scdb, &fakeCrontab{},
		sched.CreateInput{Name: "digest", Prompt: "the digest", Cron: "30 7 * * *", Cwd: testCWD, Model: "worker-test"},
		testCWD, "seed", "rig run-job", home, time.Now); err != nil {
		t.Fatal(err)
	}
	if err := scdb.DB.Close(); err != nil {
		t.Fatal(err)
	}
	ours := "30 7 * * * rig run-job j1  # pane-scheduler:j1"
	theirs := "0 12 * * * '/x/orbit' run-job j1  # pane-scheduler:j1"
	ct := &fakeCrontab{text: ours + "\n" + theirs + "\n"}
	srv, err := New(Options{Home: home, CWD: testCWD, Models: modelsTable(t), DefaultModel: "worker-test", Crontab: ct, RunnerCmd: "rig run-job", Natives: []string{"bash", "read"}, Root: home})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.stores.closeAll()
	if _, err := srv.stores.scheduler(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(ct.text, "\n"), "\n")
	if len(lines) != 2 || lines[0] != "30 7 * * * rig run-job j1  # rig-scheduler:"+sched.TagHome(home)+":j1" || lines[1] != theirs {
		t.Fatalf("the migration must run through the server's crontab, rewrite this runner's line, and leave the other runner's same-key line byte-identical:\n%s", ct.text)
	}
}
