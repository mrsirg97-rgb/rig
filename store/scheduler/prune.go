package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const pruneKeep = 20

func Prune(ctx context.Context, db DB, home string) error {
	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx)
	if err != nil {
		return err
	}
	if err := pruneRunRows(tx); err != nil {
		return err
	}
	gone, err := pruneSettledJobs(bound, tx, f)
	if err != nil {
		return err
	}
	for _, id := range gone {
		os.RemoveAll(filepath.Join(home, "runs", id))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("scheduler: prune: %w", err)
	}
	return nil
}

func pruneRunRows(tx *sql.Tx) error {
	_, err := tx.Exec(`DELETE FROM runs WHERE seq IN (
		SELECT seq FROM (SELECT seq, row_number() OVER (PARTITION BY job_id ORDER BY seq DESC) AS rn FROM runs) WHERE rn > ?)`,
		pruneKeep)
	if err != nil {
		return fmt.Errorf("scheduler: prune: %w", err)
	}
	return nil
}

func pruneSettledJobs(bound context.Context, tx *sql.Tx, f *fold) ([]string, error) {
	var settled []*jobState
	for _, j := range f.jobs {
		if j.State == "done" || j.State == "removed" {
			settled = append(settled, j)
		}
	}
	if len(settled) <= pruneKeep {
		return nil, nil
	}
	sort.Slice(settled, func(a, b int) bool { return settled[a].UpdatedSeq > settled[b].UpdatedSeq })
	keep := map[string]bool{highestJobID(f.jobs): true}
	for _, j := range settled[:pruneKeep] {
		keep[j.ID] = true
	}
	var gone []string
	for _, j := range settled {
		if !keep[j.ID] {
			gone = append(gone, j.ID)
		}
	}
	if len(gone) == 0 {
		return nil, nil
	}
	sort.Strings(gone)
	for _, id := range gone {
		if _, err := tx.Exec(`DELETE FROM runs WHERE job_id = ?`, id); err != nil {
			return nil, fmt.Errorf("scheduler: prune: %w", err)
		}
		delete(f.jobs, id)
	}
	var snapshot []any
	for _, id := range sortedJobIDs(f.jobs) {
		snapshot = append(snapshot, compactJobOf(f.jobs[id]))
	}
	if snapshot == nil {
		snapshot = []any{}
	}
	args, _ := json.Marshal(map[string]any{"jobs": snapshot})
	seq, err := appendEvent(bound, f.maxSeq+1, "compact", string(args), "")
	if err != nil {
		return nil, err
	}
	f.apply(eventRow{seq: seq, ts: nowRFC3339(), op: "compact", args: string(args)})
	if err := rewrite(tx, f); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM events WHERE seq < ?`, seq); err != nil {
		return nil, fmt.Errorf("scheduler: prune: %w", err)
	}
	return gone, nil
}

func highestJobID(jobs map[string]*jobState) string {
	best := ""
	bestNum := -1
	for id := range jobs {
		if n := jobNum(id); n > bestNum {
			best, bestNum = id, n
		}
	}
	return best
}

func sortedJobIDs(jobs map[string]*jobState) []string {
	ids := make([]string, 0, len(jobs))
	for id := range jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
