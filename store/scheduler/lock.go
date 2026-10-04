package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
)

func recordSkip(db DB, opts RunOpts, id, reason, cwd string) error {
	if opts.Decisions != nil && cwd != "" {
		state, _ := json.Marshal(map[string]string{"job": id, "reason": reason})
		opts.Decisions.Record(context.Background(), decision.Final{
			Scope:    scope.Key(cwd),
			Site:     decision.SiteScheduler,
			State:    string(state),
			Question: decision.Binary("fire", "run job "+id+" now?"),
			Answer:   "no",
			Decider:  decision.SiteScheduler,
		})
	}
	if _, err := RecordRun(context.Background(), db, RunRecordInput{
		ID: id, Status: "skip", Reason: reason,
	}); err != nil {
		return fmt.Errorf("run-job: skip record: %w", err)
	}
	return nil
}

func installRemoved(ct Crontab, text, key, home string) error {
	next, found := RemoveLine(text, key, home)
	if !found || next == text {
		return nil
	}
	return ct.Install(next)
}

func acquireLock(home, key string) (*os.File, bool, error) {
	lockDir := filepath.Join(home, "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, false, err
	}
	lockPath := filepath.Join(lockDir, strings.ReplaceAll(key, ":", "_")+".lock")
	fd, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(fd.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		fd.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("flock: %w", err)
	}
	return fd, true, nil
}

func releaseLock(fd *os.File) {
	syscall.Flock(int(fd.Fd()), syscall.LOCK_UN)
	fd.Close()
}

func hasLine(text, key, home string) bool {
	for _, l := range Scan(text, home) {
		if l.Key == key {
			return true
		}
	}
	return false
}

func pruneLogs(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".stream") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) <= keep {
		return nil
	}
	for _, name := range names[:len(names)-keep] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}
