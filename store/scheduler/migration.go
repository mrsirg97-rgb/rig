package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mrsirg97-rgb/rig/store"
)

const legacySchemaVersion = 1

var legacyStoreRe = regexp.MustCompile(`^([0-9a-f]{12})\.sqlite$`)

func Migration(home string, ct Crontab) func(*sql.Tx, int, int) (string, error) {
	return func(tx *sql.Tx, from, to int) (string, error) {
		entries, err := os.ReadDir(home)
		if err != nil {
			if os.IsNotExist(err) {
				return "", nil
			}
			return "", fmt.Errorf("scheduler: migration: %w", err)
		}
		var files []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if legacyStoreRe.MatchString(e.Name()) {
				files = append(files, e.Name())
			}
		}
		if len(files) == 0 {
			return "", nil
		}
		sort.Strings(files)

		f, err := eventsOf(tx)
		if err != nil {
			return "", fmt.Errorf("scheduler: migration: %w", err)
		}
		seq := f.maxSeq
		var maxRun int64
		if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM runs`).Scan(&maxRun); err != nil {
			return "", fmt.Errorf("scheduler: migration: %w", err)
		}
		runSeq := maxRun
		folded := 0
		repl := map[string]string{}

		for _, name := range files {
			hash := strings.TrimSuffix(name, ".sqlite")
			path := filepath.Join(home, name)
			cdb, _, _, err := store.Open(path, Statements(), legacySchemaVersion)
			if err != nil {
				return "", fmt.Errorf("scheduler: migration: %w", err)
			}
			bound, cTx, err := cdb.TxReadOnly(context.Background())
			if err != nil {
				cdb.DB.Close()
				return "", fmt.Errorf("scheduler: migration: %w", err)
			}
			cf, err := eventsOf(cTx)
			if err != nil {
				cTx.Rollback()
				cdb.DB.Close()
				return "", fmt.Errorf("scheduler: migration: %w", err)
			}
			_ = bound

			ids := make([]string, 0, len(cf.jobs))
			for id := range cf.jobs {
				ids = append(ids, id)
			}
			sort.Slice(ids, func(a, b int) bool { return jobNum(ids[a]) < jobNum(ids[b]) })
			for _, id := range ids {
				j := cf.jobs[id]
				if j.State == "removed" {
					continue
				}
				seq = f.maxSeq + 1
				newID := f.mintID()
				argsJSON, _ := json.Marshal(map[string]any{
					"id": newID, "name": j.Name, "prompt": j.Prompt,
					"cron": j.Cron, "at": j.atPtr(), "cwd": j.Cwd,
					"model": j.Model, "busy": j.Busy,
				})
				if _, err := tx.Exec(`INSERT INTO events (seq, ts, op, args, session) VALUES (?, ?, 'create', ?, NULL)`, seq, nowRFC3339(), string(argsJSON)); err != nil {
					return "", fmt.Errorf("scheduler: migration: %w", err)
				}
				f.apply(eventRow{seq: seq, ts: nowRFC3339(), op: "create", args: string(argsJSON)})

				rows, err := cTx.Query(`SELECT started_at, ended_at, status, exit, duration_ms, reason, log_path FROM runs WHERE job_id = ?`, id)
				if err != nil {
					return "", fmt.Errorf("scheduler: migration: %w", err)
				}
				for rows.Next() {
					var started, ended, status string
					var exit, dur any
					var reason, logPath any
					if err := rows.Scan(&started, &ended, &status, &exit, &dur, &reason, &logPath); err != nil {
						rows.Close()
						return "", fmt.Errorf("scheduler: migration: %w", err)
					}
					runSeq++
					if _, err := tx.Exec(`INSERT INTO runs (seq, job_id, started_at, ended_at, status, exit, duration_ms, reason, log_path) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
						runSeq, newID, started, ended, status, exit, dur, reason, logPath); err != nil {
						rows.Close()
						return "", fmt.Errorf("scheduler: migration: %w", err)
					}
				}
				if err := rows.Err(); err != nil {
					return "", fmt.Errorf("scheduler: migration: %w", err)
				}
				rows.Close()

				repl["cwd-"+hash+":"+id] = newID
				folded++
			}
			cTx.Rollback()
			cdb.DB.Close()
		}

		if err := rewrite(tx, f); err != nil {
			return "", fmt.Errorf("scheduler: migration: %w", err)
		}

		if len(repl) > 0 {
			text, err := ct.List()
			if err != nil {
				return "", fmt.Errorf("scheduler: migration: %w", err)
			}
			next := rewriteKeys(text, repl)
			if next != text {
				if err := ct.Install(next); err != nil {
					return "", fmt.Errorf("scheduler: migration: %w", err)
				}
			}
		}

		for _, name := range files {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				src := filepath.Join(home, name+suffix)
				if _, err := os.Stat(src); err != nil {
					continue
				}
				if err := os.Rename(src, filepath.Join(home, name+".migrated"+suffix)); err != nil {
					return "", fmt.Errorf("scheduler: migration: %w", err)
				}
			}
		}

		if folded > 0 {
			return fmt.Sprintf("scheduler migration: folded %d job%s, moved %d store%s", folded, plural(folded), len(files), plural(len(files))), nil
		}
		return "", nil
	}
}

func rewriteKeys(text string, repl map[string]string) string {
	olds := make([]string, 0, len(repl))
	for k := range repl {
		olds = append(olds, k)
	}
	sort.Strings(olds)
	for _, old := range olds {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(old) + `\b`)
		text = re.ReplaceAllLiteralString(text, repl[old])
	}
	return text
}

func jobNum(id string) int {
	n := 0
	for _, c := range strings.TrimPrefix(id, "j") {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
